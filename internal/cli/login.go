package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"

	"github.com/listenbox/listenbox-cli/publicapi"
)

func login(
	ctx context.Context,
	stdout io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	authPath := defaultAuthPath(home)
	previous, hasPrevious := readUsableStoredAuth(authPath)

	client, response, err := beginCLIAuthorization(ctx, config, previous, hasPrevious)
	if err != nil {
		return err
	}
	created := response.Status201
	credential, err := acceptPendingAuthorization(
		stdout,
		"",
		&publicapi.CLIAuthorizationPendingEvent{
			Type:            "cli.authorization.pending",
			VerificationUrl: created.VerificationUrl,
			Credential:      created.Credential,
		},
		config.apiOrigin,
		config.dashboardOrigin,
	)
	if err != nil {
		return err
	}
	stream, err := openCLIAuthorizationStream(ctx, client, created.Code)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	return consumeAuthorizationStreamWithCredential(
		ctx,
		stdout,
		stream,
		credential,
		config.apiOrigin,
		config.dashboardOrigin,
		authPath,
	)
}

func beginCLIAuthorization(
	ctx context.Context,
	config loadedCLIConfig,
	previous storedAuth,
	hasPrevious bool,
) (*publicapi.Client, *publicapi.CreateCLIAuthorizationResponse, error) {
	client, err := newPublicClient(config.apiOrigin, previous.APIKey, hasPrevious)
	if err != nil {
		return nil, nil, err
	}
	params := publicapi.CreateCLIAuthorizationParams{
		Body: publicapi.CreateCLIAuthorization{Scopes: requestedCLIScopes()},
	}
	response, err := client.CreateCLIAuthorization(ctx, params)
	if hasPrevious && (isUnauthorized(err) || responseUnauthorized(response)) {
		client, err = newPublicClient(config.apiOrigin, "", false)
		if err != nil {
			return nil, nil, err
		}
		response, err = client.CreateCLIAuthorization(ctx, params)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("start CLI authorization: %w", err)
	}
	if response == nil {
		return nil, nil, fmt.Errorf("start CLI authorization against %q returned no response", config.apiOrigin)
	}
	if response.Status201 == nil {
		return nil, nil, fmt.Errorf("start CLI authorization returned HTTP status %d", response.StatusCode)
	}
	return client, response, nil
}

func openCLIAuthorizationStream(
	ctx context.Context,
	client *publicapi.Client,
	code publicapi.CLIAuthorizationCode,
) (*publicapi.SSEStream[publicapi.CLIAuthorizationEvent], error) {
	response, err := client.CliAuthorizationEvents(ctx, publicapi.CliAuthorizationEventsParams{Code: code})
	if err != nil {
		return nil, fmt.Errorf("start CLI authorization event stream: %w", err)
	}
	if response == nil || response.Status200 == nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		return nil, fmt.Errorf("CLI authorization event stream returned HTTP status %d", status)
	}
	return response.Status200, nil
}

func consumeAuthorizationStreamWithCredential(
	ctx context.Context,
	stdout io.Writer,
	stream *publicapi.SSEStream[publicapi.CLIAuthorizationEvent],
	credential string,
	apiOrigin string,
	dashboardOrigin string,
	authPath string,
) error {
	for {
		event, ok, nextErr := stream.Next(ctx)
		if nextErr != nil {
			return fmt.Errorf("read CLI authorization stream: %w", nextErr)
		}
		if !ok {
			return fmt.Errorf("CLI authorization stream from %q ended before a terminal event", apiOrigin)
		}
		if event.CLIAuthorizationPendingEvent == nil {
			return finishAuthorization(ctx, event, credential, apiOrigin, authPath)
		}
		var err error
		credential, err = acceptPendingAuthorization(
			stdout,
			credential,
			event.CLIAuthorizationPendingEvent,
			apiOrigin,
			dashboardOrigin,
		)
		if err != nil {
			return err
		}
	}
}

func acceptPendingAuthorization(
	stdout io.Writer,
	currentCredential string,
	pending *publicapi.CLIAuthorizationPendingEvent,
	apiOrigin string,
	dashboardOrigin string,
) (string, error) {
	if currentCredential != "" {
		return "", fmt.Errorf("CLI authorization stream from %q emitted more than one pending event", apiOrigin)
	}
	if !validHeaderCredential(pending.Credential) {
		return "", fmt.Errorf("CLI authorization stream from %q emitted an invalid pending credential", apiOrigin)
	}
	verificationURL, err := configuredVerificationURL(pending.VerificationUrl, dashboardOrigin)
	if err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(stdout, "Verification URL: %s\n", verificationURL); err != nil {
		return "", fmt.Errorf("print verification URL %q: %w", verificationURL, err)
	}
	return pending.Credential, nil
}

func finishAuthorization(
	ctx context.Context,
	event publicapi.CLIAuthorizationEvent,
	credential string,
	apiOrigin string,
	authPath string,
) error {
	switch {
	case event.CLIAuthorizationApprovedEvent != nil:
		if credential == "" {
			return fmt.Errorf("CLI authorization stream from %q approved before its pending event", apiOrigin)
		}
		approved := event.CLIAuthorizationApprovedEvent
		if err := verifyApprovedCredential(ctx, apiOrigin, credential, approved.TeamId); err != nil {
			return err
		}
		return writeStoredAuth(authPath, storedAuth{APIKey: credential})
	case event.CLIAuthorizationDeniedEvent != nil:
		if credential == "" {
			return fmt.Errorf("CLI authorization stream from %q denied before its pending event", apiOrigin)
		}
		return fmt.Errorf("CLI authorization was denied by %q", apiOrigin)
	case event.CLIAuthorizationExpiredEvent != nil:
		if credential == "" {
			return fmt.Errorf("CLI authorization stream from %q expired before its pending event", apiOrigin)
		}
		return fmt.Errorf("CLI authorization expired against %q", apiOrigin)
	default:
		return fmt.Errorf("CLI authorization stream from %q emitted an empty event variant", apiOrigin)
	}
}

func newPublicClient(apiOrigin string, credential string, authenticated bool) (*publicapi.Client, error) {
	options := make([]publicapi.Option, 0, 1)
	if authenticated {
		options = append(options, publicapi.WithRequestEditorFn(bearerTokenRequestEditor(credential)))
	}
	client, err := publicapi.NewClient(publicapi.ClientOptions{BaseURL: apiOrigin}, options...)
	if err != nil {
		return nil, fmt.Errorf("create public API client for %q: %w", apiOrigin, err)
	}
	return client, nil
}

func bearerTokenRequestEditor(token string) publicapi.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

func isUnauthorized(err error) bool {
	var statusErr *publicapi.UnexpectedStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized
}

func responseUnauthorized(response *publicapi.CreateCLIAuthorizationResponse) bool {
	return response != nil && response.Status401
}

func validateVerificationURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parse verification URL %q: %w", value, err)
	}
	if (parsed.Scheme != httpScheme && parsed.Scheme != httpsScheme) || parsed.Host == "" {
		return fmt.Errorf("verification URL %q must be an absolute HTTP(S) URL", value)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("verification URL %q contains forbidden URL components", value)
	}
	return nil
}

func configuredVerificationURL(value string, dashboardOrigin string) (string, error) {
	if err := validateVerificationURL(value); err != nil {
		return "", err
	}
	verificationURL, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse verification URL %q after validation: %w", value, err)
	}
	dashboardURL, err := url.Parse(dashboardOrigin)
	if err != nil {
		return "", fmt.Errorf("parse configured Dashboard origin %q: %w", dashboardOrigin, err)
	}
	verificationURL.Scheme = dashboardURL.Scheme
	verificationURL.Host = dashboardURL.Host
	return verificationURL.String(), nil
}

func verifyApprovedCredential(
	ctx context.Context,
	apiOrigin string,
	credential string,
	approvedTeamID publicapi.TeamID,
) error {
	client, err := newPublicClient(apiOrigin, credential, true)
	if err != nil {
		return err
	}
	response, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("verify approved credential: %w", err)
	}
	if response.Status200 == nil {
		return fmt.Errorf("verify approved credential returned HTTP status %d", response.StatusCode)
	}
	whoami := response.Status200
	if whoami.TeamId == "" || whoami.TeamId != approvedTeamID {
		return fmt.Errorf(
			"verify approved credential team %q does not match approved team %q",
			whoami.TeamId,
			approvedTeamID,
		)
	}
	for _, scope := range requestedCLIScopes() {
		if !slices.Contains(whoami.Scopes, scope) {
			return fmt.Errorf("verify approved credential is missing scope %q", scope)
		}
	}
	return nil
}

func requestedCLIScopes() []publicapi.ApiKeyScope {
	return []publicapi.ApiKeyScope{
		publicapi.ApiKeyScopeTeamRead,
		publicapi.ApiKeyScopeTeamManage,
		publicapi.ApiKeyScopeShowRead,
		publicapi.ApiKeyScopeShowCreate,
		publicapi.ApiKeyScopeShowUpdate,
		publicapi.ApiKeyScopeEpisodeCreate,
		publicapi.ApiKeyScopeEpisodeDelete,
		publicapi.ApiKeyScopeEpisodeUpdate,
		publicapi.ApiKeyScopeEpisodePublish,
	}
}

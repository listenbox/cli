package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

const (
	configFlagArgument = "--" + configFlagName
	testEventTypeField = "type"
	testTeamIDField    = "team_id"
)

//nolint:funlen // This test intentionally verifies the complete login transaction.
func TestLoginUsesConfiguredDashboardOriginAndAtomicallyReplacesAuth(t *testing.T) {
	t.Parallel()

	const (
		previousCredential = "apk_previous.AAAAAAAAAAAAAAAAAAAAAA"
		pendingCredential  = "apk_pending.BBBBBBBBBBBBBBBBBBBBBB"
		teamID             = "team_cli_test"
	)
	requestedAuthorization := make(chan string, 1)
	requestedScopes := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/cli/authorizations":
			requestedAuthorization <- request.Header.Get("Authorization")
			var body struct {
				Scopes []string `json:"scopes"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "invalid request", http.StatusBadRequest)
				return
			}
			requestedScopes <- body.Scopes
			writeTestAuthorizationCreation(writer, request, "cla_test", pendingCredential)
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/cli/authorizations/"):
			writer.Header().Set("Content-Type", "text/event-stream")
			writeTestSSEEvent(writer, approvedTestEvent(teamID))
		case request.URL.Path == "/s/whoami":
			if request.Header.Get("Authorization") != "Bearer "+pendingCredential {
				http.Error(writer, "wrong credential", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":            "apk_pending",
				testTeamIDField: teamID,
				"scopes":        requestedScopeStrings(),
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	dashboardServer := httptest.NewServer(http.NotFoundHandler())
	defer dashboardServer.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL, dashboardServer.URL)
	authPath := defaultAuthPath(home)
	assert.NilError(t, writeStoredAuth(authPath, storedAuth{APIKey: previousCredential}))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, loginCommandName},
		&stdout,
		&stderr,
		home,
	)
	assert.NilError(t, err)
	assert.Equal(t, stderr.String(), "")
	assert.Equal(
		t,
		stdout.String(),
		"Verification URL: "+dashboardServer.URL+"/cli/authorize?code=cla_test\n",
	)
	assert.Equal(t, <-requestedAuthorization, "Bearer "+previousCredential)
	assert.DeepEqual(t, <-requestedScopes, requestedScopeStrings())

	raw, err := os.ReadFile(authPath)
	assert.NilError(t, err)
	var auth storedAuth
	assert.NilError(t, json.Unmarshal(raw, &auth))
	assert.DeepEqual(t, auth, storedAuth{APIKey: pendingCredential})
	var fields map[string]any
	assert.NilError(t, json.Unmarshal(raw, &fields))
	_, hasAPIOrigin := fields["api_origin"]
	assert.Assert(t, !hasAPIOrigin)
	authInfo, err := os.Stat(authPath)
	assert.NilError(t, err)
	assert.Equal(t, authInfo.Mode().Perm(), os.FileMode(0o600))
	directoryInfo, err := os.Stat(filepath.Dir(authPath))
	assert.NilError(t, err)
	assert.Equal(t, directoryInfo.Mode().Perm(), os.FileMode(0o700))
}

func TestLoginDenialPreservesPreviousAuth(t *testing.T) {
	t.Parallel()

	const previousCredential = "apk_previous.AAAAAAAAAAAAAAAAAAAAAA"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			writeTestAuthorizationCreation(writer, request, "cla_denied", "apk_pending.BBBBBBBBBBBBBBBBBBBBBB")
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writeTestSSEEvent(writer, deniedTestEvent())
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	authPath := defaultAuthPath(home)
	previous := storedAuth{APIKey: previousCredential}
	assert.NilError(t, writeStoredAuth(authPath, previous))

	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{loginCommandName, configFlagArgument, configPath},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.ErrorContains(t, err, "authorization was denied")
	assert.Equal(
		t,
		stdout.String(),
		"Verification URL: "+server.URL+"/cli/authorize?code=cla_denied\n",
	)
	auth, ok := readUsableStoredAuth(authPath)
	assert.Assert(t, ok)
	assert.DeepEqual(t, auth, previous)
}

func TestLoginDoesNotWriteAuthWithoutApproval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal map[string]string
		want     string
	}{
		{
			name:     "expired",
			terminal: map[string]string{testEventTypeField: "cli.authorization.expired"},
			want:     "authorization expired",
		},
		{
			name: "stream interruption",
			want: "ended before a terminal event",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					writeTestAuthorizationCreation(writer, request, "cla_terminal", "apk_pending.BBBBBBBBBBBBBBBBBBBBBB")
					return
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				if testCase.terminal != nil {
					writeTestSSEEvent(writer, testCase.terminal)
				}
			}))
			defer server.Close()

			home := t.TempDir()
			configPath := writeTestCLIConfig(t, home, server.URL)
			err := runCLIWithHome(
				context.Background(),
				[]string{configFlagArgument, configPath, loginCommandName},
				&bytes.Buffer{},
				&bytes.Buffer{},
				home,
			)
			assert.ErrorContains(t, err, testCase.want)
			_, statErr := os.Stat(defaultAuthPath(home))
			assert.Assert(t, os.IsNotExist(statErr))
		})
	}
}

func TestLoginRetriesAnInvalidPreviousCredentialAnonymously(t *testing.T) {
	t.Parallel()

	//nolint:gosec // Synthetic credential used only by the local test server.
	const previousCredential = "apk_stale.AAAAAAAAAAAAAAAAAAAAAA"
	requestedAuthorization := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			authorization := request.Header.Get("Authorization")
			requestedAuthorization <- authorization
			if authorization != "" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeTestAuthorizationCreation(writer, request, "cla_retry", "apk_pending.BBBBBBBBBBBBBBBBBBBBBB")
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writeTestSSEEvent(writer, deniedTestEvent())
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	assert.NilError(t, writeStoredAuth(
		defaultAuthPath(home),
		storedAuth{APIKey: previousCredential},
	))
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, loginCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		home,
	)
	assert.ErrorContains(t, err, "authorization was denied")
	assert.Equal(t, <-requestedAuthorization, "Bearer "+previousCredential)
	assert.Equal(t, <-requestedAuthorization, "")
}

func TestLoginDoesNotWriteAuthWhenFinalWhoamiRejectsCredential(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/cli/authorizations":
			writeTestAuthorizationCreation(writer, request, "cla_rejected", "apk_rejected.BBBBBBBBBBBBBBBBBBBBBB")
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/cli/authorizations/"):
			writer.Header().Set("Content-Type", "text/event-stream")
			writeTestSSEEvent(writer, approvedTestEvent("team_rejected"))
		case request.URL.Path == "/s/whoami":
			writer.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, loginCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		home,
	)
	assert.ErrorContains(t, err, "verify approved credential returned HTTP status 401")
	_, statErr := os.Stat(defaultAuthPath(home))
	assert.Assert(t, os.IsNotExist(statErr))
}

func writeTestCLIConfig(
	t *testing.T,
	directory string,
	apiOrigin string,
	dashboardOrigins ...string,
) string {
	t.Helper()
	dashboardOrigin := apiOrigin
	if len(dashboardOrigins) == 1 {
		dashboardOrigin = dashboardOrigins[0]
	}
	path := filepath.Join(directory, "cli.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(
		"api_origin: "+apiOrigin+"\n"+
			"dashboard_origin: "+dashboardOrigin+"\n"+
			"print_trace_ids: false\n",
	), 0o600))
	return path
}

func serverURL(request *http.Request) string {
	return httpScheme + "://" + request.Host
}

func writeTestAuthorizationCreation(writer http.ResponseWriter, request *http.Request, code string, credential string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(writer).Encode(map[string]string{
		"code":             code,
		"verification_url": serverURL(request) + "/cli/authorize?code=" + code,
		"credential":       credential,
	}); err != nil {
		panic(err)
	}
}

func approvedTestEvent(teamID string) map[string]string {
	return map[string]string{
		testEventTypeField: "cli.authorization.approved",
		testTeamIDField:    teamID,
	}
}

func deniedTestEvent() map[string]string {
	return map[string]string{testEventTypeField: "cli.authorization.denied"}
}

func writeTestSSEEvent(writer io.Writer, event any) {
	_, _ = io.WriteString(writer, "data: ")
	if err := json.NewEncoder(writer).Encode(event); err != nil {
		return
	}
	_, _ = io.WriteString(writer, "\n")
}

func requestedScopeStrings() []string {
	return []string{
		"team:read",
		"team:manage",
		"show:read",
		"show:create",
		"show:update",
		"episode:create",
		"episode:delete",
		"episode:update",
		"episode:publish",
	}
}

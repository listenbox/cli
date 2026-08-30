package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
	"github.com/mattn/go-isatty"
)

const (
	importProgressBarWidth  = 24
	importProgressEventType = "progress"
	importTerminalEventType = "terminal"
	importCompletedStatus   = "completed"
	importCancelledStatus   = "cancelled"
	importFailedStatus      = "failed"
	maxImportPercent        = 100
)

func importPodcast(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
	sourceURL string,
	slug string,
	slugExplicit bool,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("import RSS feed: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	body := importRSSRequest(sourceURL, slug, slugExplicit)
	response, err := client.ImportRSS(ctx, publicapi.ImportRSSParams{Body: body})
	if err != nil {
		return fmt.Errorf("import RSS feed %q: %w", sourceURL, err)
	}
	if response == nil {
		return fmt.Errorf("import RSS feed %q against %q returned no response", sourceURL, config.apiOrigin)
	}
	if response.Status202 != nil {
		if config.printTraceIDs {
			if err := printImportTraceID(stdout, response.Raw.Header.Get("X-Trace-Id")); err != nil {
				return err
			}
		}
		return consumeImportRSSRun(ctx, stdout, stderr, sourceURL, config, client, response)
	}
	return handleImportRSSResponse(
		sourceURL,
		slug,
		slugExplicit,
		response,
	)
}

func importRSSRequest(sourceURL string, slug string, slugExplicit bool) publicapi.ImportRSSRequest {
	body := publicapi.ImportRSSRequest{SourceUrl: sourceURL}
	if slugExplicit {
		body.Slug = &slug
	}
	return body
}

func consumeImportRSSRun(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	sourceURL string,
	config loadedCLIConfig,
	client *publicapi.Client,
	response *publicapi.ImportRSSResponse,
) error {
	eventsResponse, err := client.ImportRSSRunEvents(ctx, publicapi.ImportRSSRunEventsParams{
		ImportRunId: response.Status202.ImportRunId,
	})
	if err != nil {
		return fmt.Errorf("read RSS import event stream for %q: %w", sourceURL, err)
	}
	if eventsResponse == nil || eventsResponse.Status200 == nil {
		status := 0
		if eventsResponse != nil {
			status = eventsResponse.StatusCode
		}
		return fmt.Errorf("RSS import event stream for %q returned HTTP status %d", sourceURL, status)
	}
	return consumeImportRSSStream(
		ctx,
		stdout,
		stderr,
		sourceURL,
		config.dashboardOrigin,
		eventsResponse.Status200,
	)
}

func handleImportRSSResponse(
	sourceURL string,
	requestedSlug string,
	slugExplicit bool,
	response *publicapi.ImportRSSResponse,
) error {
	switch {
	case response.Status202 != nil:
		return fmt.Errorf("import RSS feed %q returned a run without an event stream", sourceURL)
	case response.Status400 != nil:
		return fmt.Errorf("import RSS feed %q: %s", sourceURL, response.Status400.Message)
	case response.Status402 != nil:
		return fmt.Errorf(
			"import RSS feed %q: %s import requires the %s entitlement (current: %s); upgrade at %s and retry",
			sourceURL,
			response.Status402.MediaKind,
			response.Status402.RequiredEntitlement,
			response.Status402.CurrentEntitlement,
			response.Status402.PricingUrl,
		)
	case response.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("import RSS feed %q: authentication failed; run listenbox login", sourceURL)
	case response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("import RSS feed %q: API key lacks show:create scope", sourceURL)
	case response.StatusCode == http.StatusConflict && slugExplicit:
		return fmt.Errorf("import RSS feed %q: requested slug %q conflicts", sourceURL, requestedSlug)
	case response.StatusCode == http.StatusConflict:
		return fmt.Errorf("import RSS feed %q: import choice conflicts", sourceURL)
	default:
		return fmt.Errorf("import RSS feed %q returned HTTP status %d", sourceURL, response.StatusCode)
	}
}

func printImportTraceID(stdout io.Writer, traceID string) error {
	decoded, err := hex.DecodeString(traceID)
	if err != nil || len(decoded) != 16 || traceID != strings.ToLower(traceID) || strings.Trim(traceID, "0") == "" {
		return fmt.Errorf("print RSS import trace ID: invalid trace_id %q", traceID)
	}
	if _, err := fmt.Fprintf(stdout, "Trace ID: %s\n", traceID); err != nil {
		return fmt.Errorf("print RSS import trace ID %q: %w", traceID, err)
	}
	return nil
}

func consumeImportRSSStream(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	sourceURL string,
	dashboardOrigin string,
	stream *publicapi.SSEStream[publicapi.PublicRSSImportEvent],
) error {
	defer func() { _ = stream.Close() }()
	progress := newImportProgress(stderr, writerIsTerminal(stderr))
	defer progress.cleanFailureLine()
	for {
		event, ok, err := stream.Next(ctx)
		if err != nil {
			return fmt.Errorf("read RSS import stream for %q: %w", sourceURL, err)
		}
		if !ok {
			return fmt.Errorf("RSS import stream for %q ended before a terminal event", sourceURL)
		}
		done, err := handleImportRSSEvent(stdout, stderr, sourceURL, dashboardOrigin, progress, event)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func handleImportRSSEvent(
	stdout io.Writer,
	stderr io.Writer,
	sourceURL string,
	dashboardOrigin string,
	progress *importProgress,
	event publicapi.PublicRSSImportEvent,
) (bool, error) {
	if event.RSSImportProgressEvent != nil {
		current := event.RSSImportProgressEvent
		if current.Type != importProgressEventType || current.Percent < 0 || current.Percent > maxImportPercent {
			return false, fmt.Errorf("RSS import stream for %q emitted malformed progress event", sourceURL)
		}
		return false, progress.update(uint32(current.Percent))
	}
	if event.PublicRSSImportTerminalEvent == nil {
		return false, fmt.Errorf("RSS import stream for %q emitted an empty event variant", sourceURL)
	}
	return handleImportRSSTerminal(
		stdout,
		stderr,
		sourceURL,
		dashboardOrigin,
		progress,
		event.PublicRSSImportTerminalEvent,
	)
}

func handleImportRSSTerminal(
	stdout io.Writer,
	stderr io.Writer,
	sourceURL string,
	dashboardOrigin string,
	progress *importProgress,
	terminal *publicapi.PublicRSSImportTerminalEvent,
) (bool, error) {
	if terminal.PublicRSSImportCompletedTerminalEvent != nil {
		return finishCompletedRSSImport(
			stdout,
			stderr,
			sourceURL,
			dashboardOrigin,
			progress,
			terminal.PublicRSSImportCompletedTerminalEvent,
		)
	}
	if terminal.RSSImportCancelledTerminalEvent != nil {
		cancelled := terminal.RSSImportCancelledTerminalEvent
		if cancelled.Type != importTerminalEventType || cancelled.Status != importCancelledStatus {
			return false, fmt.Errorf("RSS import stream for %q emitted malformed cancelled terminal event", sourceURL)
		}
		return false, fmt.Errorf("RSS import feed %q was cancelled", sourceURL)
	}
	if terminal.RSSImportFailedTerminalEvent == nil {
		return false, fmt.Errorf("RSS import stream for %q emitted an empty terminal event variant", sourceURL)
	}
	return false, failedRSSImportError(sourceURL, terminal.RSSImportFailedTerminalEvent)
}

func finishCompletedRSSImport(
	stdout io.Writer,
	stderr io.Writer,
	sourceURL string,
	dashboardOrigin string,
	progress *importProgress,
	completed *publicapi.PublicRSSImportCompletedTerminalEvent,
) (bool, error) {
	if completed.Type != importTerminalEventType || completed.Status != importCompletedStatus ||
		!validCompletionID(completed.FeedId, "lb_") || !validShowSlug(completed.ShowSlug) {
		return false, fmt.Errorf("RSS import stream for %q emitted malformed completed terminal event", sourceURL)
	}
	showURL, err := showManagementURL(dashboardOrigin, completed.TeamId, completed.ShowId)
	if err != nil {
		return false, fmt.Errorf(
			"RSS import stream for %q emitted malformed completed terminal event: %w",
			sourceURL,
			err,
		)
	}
	if err := progress.complete(); err != nil {
		return false, err
	}
	if _, err := fmt.Fprintln(stdout, completed.ShowSlug); err != nil {
		return false, fmt.Errorf("print imported show slug %q: %w", completed.ShowSlug, err)
	}
	if _, err := fmt.Fprintf(stderr, "Open in Listenbox: %s\n", showURL); err != nil {
		return false, fmt.Errorf("print imported show URL %q: %w", showURL, err)
	}
	return true, nil
}

func showManagementURL(dashboardOrigin string, teamID string, showID string) (string, error) {
	normalizedOrigin, err := normalizeOrigin(dashboardOrigin, "Dashboard")
	if err != nil {
		return "", err
	}
	if !validCompletionID(teamID, "team_") {
		return "", fmt.Errorf("invalid team_id %q", teamID)
	}
	if !validCompletionID(showID, "shw_") {
		return "", fmt.Errorf("invalid show_id %q", showID)
	}
	showURL, err := url.JoinPath(normalizedOrigin, teamID, "shows", showID)
	if err != nil {
		return "", fmt.Errorf(
			"build show URL from Dashboard origin %q, team ID %q, and show ID %q: %w",
			normalizedOrigin,
			teamID,
			showID,
			err,
		)
	}
	return showURL, nil
}

func validCompletionID(value string, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) > len(prefix) &&
		strings.TrimSpace(value) == value && url.PathEscape(value) == value
}

func validShowSlug(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	previousHyphen := true
	for _, current := range value {
		switch {
		case current >= 'a' && current <= 'z', current >= '0' && current <= '9':
			previousHyphen = false
		case current == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func failedRSSImportError(sourceURL string, failed *publicapi.RSSImportFailedTerminalEvent) error {
	if failed.Type != importTerminalEventType || failed.Status != importFailedStatus ||
		strings.TrimSpace(failed.ErrorCode) == "" || strings.TrimSpace(failed.ErrorMessage) == "" {
		return fmt.Errorf("RSS import stream for %q emitted malformed failed terminal event", sourceURL)
	}
	return fmt.Errorf(
		"RSS import feed %q failed (%s): %s",
		sourceURL,
		failed.ErrorCode,
		failed.ErrorMessage,
	)
}

type importProgress struct {
	writer   io.Writer
	tty      bool
	hasValue bool
	last     uint32
	lineOpen bool
}

func newImportProgress(writer io.Writer, tty bool) *importProgress {
	return &importProgress{writer: writer, tty: tty}
}

func (progress *importProgress) update(percent uint32) error {
	if progress.hasValue && progress.last == percent {
		return nil
	}
	progress.hasValue = true
	progress.last = percent
	if !progress.tty {
		if _, err := fmt.Fprintf(progress.writer, "Import progress: %d%%\n", percent); err != nil {
			return fmt.Errorf("print RSS import progress %d%%: %w", percent, err)
		}
		return nil
	}
	filled := int(percent) * importProgressBarWidth / maxImportPercent
	bar := strings.Repeat("#", filled) + strings.Repeat("-", importProgressBarWidth-filled)
	if _, err := fmt.Fprintf(progress.writer, "\rImporting [%s] %3d%%", bar, percent); err != nil {
		return fmt.Errorf("print RSS import progress %d%%: %w", percent, err)
	}
	progress.lineOpen = true
	return nil
}

func (progress *importProgress) complete() error {
	if err := progress.update(maxImportPercent); err != nil {
		return err
	}
	if progress.tty && progress.lineOpen {
		if _, err := fmt.Fprintln(progress.writer); err != nil {
			return fmt.Errorf("finish RSS import progress line: %w", err)
		}
		progress.lineOpen = false
	}
	return nil
}

func (progress *importProgress) cleanFailureLine() {
	if progress.tty && progress.lineOpen {
		_, _ = fmt.Fprintln(progress.writer)
		progress.lineOpen = false
	}
}

func writerIsTerminal(writer io.Writer) bool {
	descriptor, ok := writer.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return isatty.IsTerminal(descriptor.Fd()) || isatty.IsCygwinTerminal(descriptor.Fd())
}

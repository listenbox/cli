//nolint:lll
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

const (
	showDeletionProgressEventType = "progress"
	showDeletionTerminalEventType = "terminal"
	showDeletionCompletedStatus   = "completed"
	showDeletionFailedStatus      = "failed"
	processingStatus              = "processing"
	maxShowDeletionPercent        = 100
)

func deleteShow(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
	slug string,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("delete show: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	response, err := client.CreateShowDeletion(ctx, publicapi.CreateShowDeletionParams{ShowSlug: slug})
	if err != nil {
		return fmt.Errorf("delete show %q: %w", slug, err)
	}
	created, err := validateShowDeletionResponse(slug, response)
	if err != nil {
		return err
	}
	eventsResponse, err := client.ShowDeletionEvents(ctx, publicapi.ShowDeletionEventsParams{
		ShowDeletionRunId: created.ShowDeletionRunId,
	})
	if err != nil {
		return fmt.Errorf("read show deletion event stream for %q: %w", slug, err)
	}
	stream, err := validateShowDeletionEventsResponse(slug, eventsResponse)
	if err != nil {
		return err
	}
	return consumeShowDeletionStream(ctx, stdout, stderr, slug, stream)
}

func validateShowDeletionResponse(slug string, response *publicapi.CreateShowDeletionResponse) (*publicapi.CreatedPublicShowDeletion, error) {
	if response == nil {
		return nil, fmt.Errorf("delete show %q returned no response", slug)
	}
	if response.Status202 != nil {
		return response.Status202, nil
	}
	switch {
	case response.Status400 != nil:
		return nil, fmt.Errorf("delete show %q: %s", slug, response.Status400.Message)
	case response.Status401:
		return nil, fmt.Errorf("delete show %q: authentication failed; run listenbox login", slug)
	case response.Status403:
		return nil, fmt.Errorf("delete show %q: only the current show owner may delete it", slug)
	case response.Status404:
		return nil, fmt.Errorf("delete show %q: show not found", slug)
	default:
		return nil, fmt.Errorf("delete show %q returned HTTP status %d", slug, response.StatusCode)
	}
}

func validateShowDeletionEventsResponse(slug string, response *publicapi.ShowDeletionEventsResponse) (*publicapi.SSEStream[publicapi.ShowDeletionEvent], error) {
	if response == nil || response.Status200 == nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		return nil, fmt.Errorf("show deletion event stream for %q returned HTTP status %d", slug, status)
	}
	return response.Status200, nil
}

func consumeShowDeletionStream(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	slug string,
	stream *publicapi.SSEStream[publicapi.ShowDeletionEvent],
) error {
	defer func() { _ = stream.Close() }()
	progress := newImportProgress(stderr, writerIsTerminal(stderr))
	defer progress.cleanFailureLine()
	for {
		event, ok, err := stream.Next(ctx)
		if err != nil {
			return fmt.Errorf("read show deletion stream for %q: %w", slug, err)
		}
		if !ok {
			return fmt.Errorf("show deletion stream for %q ended before a terminal event", slug)
		}
		done, err := handleShowDeletionEvent(stdout, slug, progress, event)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func handleShowDeletionEvent(
	stdout io.Writer,
	slug string,
	progress *importProgress,
	event publicapi.ShowDeletionEvent,
) (bool, error) {
	if current := event.ShowDeletionProgressEvent; current != nil {
		if !validShowDeletionProgressEvent(*current) {
			return false, fmt.Errorf("show deletion stream for %q emitted malformed progress event", slug)
		}
		percent := uint32(current.Percent) //nolint:gosec // validated non-negative and bounded above
		if err := progress.update(percent); err != nil {
			return false, err
		}
		return false, nil
	}
	if event.ShowDeletionTerminalEvent == nil {
		return false, fmt.Errorf("show deletion stream for %q emitted an empty event variant", slug)
	}
	return handleShowDeletionTerminalEvent(stdout, slug, progress, *event.ShowDeletionTerminalEvent)
}

func validShowDeletionProgressEvent(event publicapi.ShowDeletionProgressEvent) bool {
	if event.Type != showDeletionProgressEventType || event.Percent < 0 || event.Percent > maxShowDeletionPercent {
		return false
	}
	if event.Status != "queued" && event.Status != processingStatus {
		return false
	}
	return strings.TrimSpace(event.CurrentStep) != ""
}

func handleShowDeletionTerminalEvent(
	stdout io.Writer,
	slug string,
	progress *importProgress,
	event publicapi.ShowDeletionTerminalEvent,
) (bool, error) {
	if completed := event.ShowDeletionCompletedEvent; completed != nil {
		if completed.Type != showDeletionTerminalEventType ||
			completed.Status != showDeletionCompletedStatus || completed.ShowSlug != slug {
			return false, fmt.Errorf("show deletion stream for %q emitted malformed completed terminal event", slug)
		}
		if err := progress.complete(); err != nil {
			return false, err
		}
		if _, err := fmt.Fprintf(stdout, "Deleted show %q\n", slug); err != nil {
			return false, fmt.Errorf("print deleted show %q: %w", slug, err)
		}
		return true, nil
	}
	failed := event.ShowDeletionFailedEvent
	if failed == nil || failed.Type != showDeletionTerminalEventType || failed.Status != showDeletionFailedStatus ||
		strings.TrimSpace(failed.ErrorCode) == "" || strings.TrimSpace(failed.ErrorMessage) == "" {
		return false, fmt.Errorf("show deletion stream for %q emitted malformed failed terminal event", slug)
	}
	return false, fmt.Errorf("delete show %q failed (%s): %s", slug, failed.ErrorCode, failed.ErrorMessage)
}

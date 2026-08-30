package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

func runEpisodesDeleteCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox episodes delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	episodeID := ""
	yes := false
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&episodeID, episodeFlagName, "", "episode ID (required)")
	flags.BoolVar(&yes, yesFlagName, false, "confirm permanent deletion (required)")
	flags.Usage = func() { writeEpisodesDeleteUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse episodes delete flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("episodes delete received unexpected argument %q", flags.Arg(0))
	}
	if !validCompletionID(episodeID, "ep_") {
		return fmt.Errorf("episodes delete: invalid --episode %q", episodeID)
	}
	if !flagWasSet(flags, yesFlagName) || !yes {
		return errors.New("episodes delete: --yes is required")
	}
	return deleteEpisode(ctx, stdout, stderr, home, configPath, configExplicit, defaultConfig, episodeID)
}

//nolint:cyclop,funlen,gocognit,gocyclo // The CLI validates each HTTP and SSE variant before reporting deletion.
func deleteEpisode(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
	episodeID string,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return errors.New("delete episode: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	response, err := client.CreateEpisodeDeletion(ctx, publicapi.CreateEpisodeDeletionParams{EpisodeId: episodeID})
	if err != nil {
		return fmt.Errorf("delete episode %q: %w", episodeID, err)
	}
	if response == nil || response.Status202 == nil {
		if response != nil {
			switch {
			case response.Status401:
				return fmt.Errorf("delete episode %q: authentication failed; run listenbox login", episodeID)
			case response.Status403:
				return fmt.Errorf("delete episode %q: only the current team owner may delete it", episodeID)
			case response.Status404:
				return fmt.Errorf("delete episode %q: episode not found", episodeID)
			}
			return fmt.Errorf("delete episode %q returned HTTP status %d", episodeID, response.StatusCode)
		}
		return fmt.Errorf("delete episode %q returned no response", episodeID)
	}
	eventsResponse, err := client.EpisodeDeletionEvents(ctx, publicapi.EpisodeDeletionEventsParams{
		EpisodeDeletionRunId: response.Status202.EpisodeDeletionRunId,
	})
	if err != nil {
		return fmt.Errorf("read episode deletion event stream for %q: %w", episodeID, err)
	}
	if eventsResponse == nil || eventsResponse.Status200 == nil {
		status := 0
		if eventsResponse != nil {
			status = eventsResponse.StatusCode
		}
		return fmt.Errorf("episode deletion event stream for %q returned HTTP status %d", episodeID, status)
	}
	stream := eventsResponse.Status200
	defer func() { _ = stream.Close() }()
	progress := newImportProgress(stderr, writerIsTerminal(stderr))
	defer progress.cleanFailureLine()
	for {
		event, ok, err := stream.Next(ctx)
		if err != nil {
			return fmt.Errorf("read episode deletion stream for %q: %w", episodeID, err)
		}
		if !ok {
			return fmt.Errorf("episode deletion stream for %q ended before a terminal event", episodeID)
		}
		if current := event.EpisodeDeletionProgressEvent; current != nil {
			if current.Type != showDeletionProgressEventType || current.Percent < 0 ||
				current.Percent > maxShowDeletionPercent || strings.TrimSpace(current.CurrentStep) == "" {
				return fmt.Errorf("episode deletion stream for %q emitted malformed progress event", episodeID)
			}
			if err := progress.update(uint32(current.Percent)); err != nil {
				return err
			}
			continue
		}
		terminal := event.EpisodeDeletionTerminalEvent
		if terminal == nil {
			return fmt.Errorf("episode deletion stream for %q emitted an empty event variant", episodeID)
		}
		if completed := terminal.EpisodeDeletionCompletedEvent; completed != nil {
			if completed.Type != showDeletionTerminalEventType || completed.Status != showDeletionCompletedStatus ||
				completed.EpisodeId != episodeID {
				return fmt.Errorf("episode deletion stream for %q emitted malformed completed event", episodeID)
			}
			if err := progress.complete(); err != nil {
				return err
			}
			_, err := fmt.Fprintf(stdout, "Deleted episode %q\n", episodeID)
			return err
		}
		failed := terminal.EpisodeDeletionFailedEvent
		if failed == nil || strings.TrimSpace(failed.ErrorCode) == "" || strings.TrimSpace(failed.ErrorMessage) == "" {
			return fmt.Errorf("episode deletion stream for %q emitted malformed failed event", episodeID)
		}
		return fmt.Errorf("delete episode %q failed (%s): %s", episodeID, failed.ErrorCode, failed.ErrorMessage)
	}
}

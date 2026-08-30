package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

func runShowsDeleteCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox shows delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	slug := ""
	yes := false
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&slug, showFlagName, "", "global show slug (required)")
	flags.BoolVar(&yes, yesFlagName, false, "confirm permanent deletion (required)")
	flags.Usage = func() { writeShowsDeleteUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse shows delete flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("shows delete received unexpected argument %q", flags.Arg(0))
	}
	if slug == "" {
		return errors.New("shows delete: --show is required")
	}
	if !validShowSlug(slug) {
		return fmt.Errorf("shows delete: invalid --show %q", slug)
	}
	if !flagWasSet(flags, yesFlagName) || !yes {
		return errors.New("shows delete: --yes is required")
	}
	return deleteShow(
		ctx,
		stdout,
		stderr,
		home,
		configPath,
		configExplicit,
		defaultConfig,
		slug,
	)
}

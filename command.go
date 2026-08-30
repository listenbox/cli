package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/listenbox/listenbox-cli/model"
)

const (
	configFlagName      = "config"
	titleFlagName       = "title"
	slugFlagName        = "slug"
	typeFlagName        = "type"
	showFlagName        = "show"
	episodeFlagName     = "episode"
	descriptionFlagName = "description"
	fileFlagName        = "file"
	artworkFlagName     = "artwork"
	languageFlagName    = "language"
	limitFlagName       = "limit"
	helpCommandName     = "help"
	shortHelpFlag       = "-h"
	longHelpFlag        = "--help"
	importCommandName   = "import"
	authCommandName     = "auth"
	loginCommandName    = "login"
	statusCommandName   = "status"
	showsCommandName    = "shows"
	episodesCommandName = "episodes"
	membersCommandName  = "members"
	listCommandName     = "list"
	createCommandName   = "create"
	deleteCommandName   = "delete"
	inviteCommandName   = "invite"
	roleCommandName     = "role"
	removeCommandName   = "remove"
	yesFlagName         = "yes"
	nestedCommandArgs   = 2
)

func runCLI(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home directory: %w", err)
	}
	return runCLIWithHome(ctx, args, stdout, stderr, home)
}

func runCLIWithHome(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
) error {
	defaultConfig, err := loadEmbeddedCLIConfig()
	if err != nil {
		return err
	}
	rootFlags := flag.NewFlagSet("listenbox", flag.ContinueOnError)
	rootFlags.SetOutput(stderr)
	configPath := ""
	rootFlags.StringVar(&configPath, configFlagName, "", "path to CLI config file")
	rootFlags.Usage = func() { writeRootUsage(stderr) }
	if err := rootFlags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse listenbox flags: %w", err)
	}

	configExplicit := flagWasSet(rootFlags, configFlagName)
	remaining := rootFlags.Args()
	if len(remaining) == 0 {
		writeRootUsage(stderr)
		return fmt.Errorf("command is required in arguments %v", args)
	}
	return dispatchCLICommand(
		ctx,
		remaining,
		stdout,
		stderr,
		home,
		configPath,
		configExplicit,
		defaultConfig,
	)
}

func dispatchCLICommand(
	ctx context.Context,
	remaining []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	switch remaining[0] {
	case helpCommandName:
		return runHelp(remaining[1:], stdout)
	case loginCommandName:
		return runLoginCommand(
			ctx,
			remaining[1:],
			stdout,
			stderr,
			home,
			configPath,
			configExplicit,
			defaultConfig,
		)
	case authCommandName:
		return runAuthCommand(
			ctx,
			remaining[1:],
			stdout,
			stderr,
			home,
			configPath,
			configExplicit,
			defaultConfig,
		)
	case importCommandName:
		return runImportCommand(
			ctx,
			remaining[1:],
			stdout,
			stderr,
			home,
			configPath,
			configExplicit,
			defaultConfig,
		)
	case showsCommandName, episodesCommandName, membersCommandName:
		return dispatchTeamCommand(
			ctx, remaining, stdout, stderr, home, configPath, configExplicit, defaultConfig,
		)
	default:
		writeRootUsage(stderr)
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func dispatchTeamCommand(
	ctx context.Context,
	remaining []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	args := remaining[1:]
	switch remaining[0] {
	case showsCommandName:
		return runShowsCommand(ctx, args, stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case episodesCommandName:
		return runEpisodesCommand(ctx, args, stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case membersCommandName:
		return runMembersCommand(ctx, args, stdout, stderr, home, configPath, configExplicit, defaultConfig)
	default:
		return fmt.Errorf("unknown team command %q", remaining[0])
	}
}

func runAuthCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	if len(args) == 0 {
		writeAuthUsage(stderr)
		return errors.New("auth command is required")
	}
	switch args[0] {
	case helpCommandName, shortHelpFlag, longHelpFlag:
		if len(args) == 1 {
			writeAuthUsage(stdout)
			return nil
		}
		if len(args) == nestedCommandArgs && writeAuthCommandUsage(args[1], stdout) {
			return nil
		}
		return fmt.Errorf("auth help received unknown command %q", args[1])
	case statusCommandName:
		return runAuthStatusCommand(
			ctx,
			args[1:],
			stdout,
			stderr,
			home,
			configPath,
			configExplicit,
			defaultConfig,
		)
	default:
		writeAuthUsage(stderr)
		return fmt.Errorf("unknown auth command %q", args[0])
	}
}

func runImportCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	slug := ""
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&slug, slugFlagName, "", "requested global show slug")
	flags.Usage = func() { writeImportUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse import flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	slugExplicit := flagWasSet(flags, slugFlagName)
	switch flags.NArg() {
	case 0:
		return errors.New("import requires exactly one source URL")
	case 1:
	default:
		return fmt.Errorf("import received unexpected argument %q", flags.Arg(1))
	}
	sourceURL := flags.Arg(0)
	return importPodcast(
		ctx,
		stdout,
		stderr,
		home,
		configPath,
		configExplicit,
		defaultConfig,
		sourceURL,
		slug,
		slugExplicit,
	)
}

func runShowsCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	if len(args) == 0 {
		writeShowsUsage(stderr)
		return errors.New("shows command is required")
	}
	if isShowsHelpCommand(args[0]) {
		return runShowsHelpCommand(args, stdout)
	}
	switch args[0] {
	case listCommandName:
		return runShowsListCommand(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case createCommandName:
		return runShowsCreateCommand(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case deleteCommandName:
		return runShowsDeleteCommand(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	default:
		writeShowsUsage(stderr)
		return fmt.Errorf("unknown shows command %q", args[0])
	}
}

func isShowsHelpCommand(value string) bool {
	return value == helpCommandName || value == shortHelpFlag || value == longHelpFlag
}

func runShowsHelpCommand(args []string, stdout io.Writer) error {
	if len(args) == 1 {
		writeShowsUsage(stdout)
		return nil
	}
	if len(args) != nestedCommandArgs {
		return fmt.Errorf("shows help received unknown command %q", args[1])
	}
	switch args[1] {
	case createCommandName:
		writeShowsCreateUsage(stdout)
	case listCommandName:
		writeShowsListUsage(stdout)
	case deleteCommandName:
		writeShowsDeleteUsage(stdout)
	default:
		return fmt.Errorf("shows help received unknown command %q", args[1])
	}
	return nil
}

func runShowsListCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox shows list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.Usage = func() { writeShowsListUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse shows list flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("shows list received unexpected argument %q", flags.Arg(0))
	}
	return listShows(ctx, stdout, home, configPath, configExplicit, defaultConfig)
}

func runShowsCreateCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox shows create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	arguments := showsCreateArguments{}
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&arguments.Title, titleFlagName, "", "show title (required)")
	flags.StringVar(&arguments.Slug, slugFlagName, "", "global show slug (required)")
	flags.StringVar(&arguments.Type, typeFlagName, "", "kind of podcast: audio or video (required)")
	flags.StringVar(&arguments.Artwork, artworkFlagName, "", "path to optional show artwork")
	flags.StringVar(&arguments.Language, languageFlagName, "", "podcast language code")
	flags.Usage = func() { writeShowsCreateUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse shows create flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("shows create received unexpected argument %q", flags.Arg(0))
	}
	if err := validateShowsCreateArguments(&arguments); err != nil {
		return err
	}
	podcastType, err := model.NewPodcastTypeFromValue(arguments.Type)
	if err != nil {
		return fmt.Errorf("shows create: %w", err)
	}

	return createShow(
		ctx,
		stdout,
		home,
		configPath,
		configExplicit,
		defaultConfig,
		arguments.Title,
		arguments.Slug,
		podcastType,
		arguments.Artwork,
		arguments.Language,
	)
}

func runLoginCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	loginFlags := flag.NewFlagSet("listenbox login", flag.ContinueOnError)
	loginFlags.SetOutput(stderr)
	loginFlags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	loginFlags.Usage = func() { writeLoginUsage(stderr) }
	if err := loginFlags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse login flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(loginFlags, configFlagName)
	if loginFlags.NArg() != 0 {
		return fmt.Errorf("login received unexpected argument %q", loginFlags.Arg(0))
	}

	return login(ctx, stdout, home, configPath, configExplicit, defaultConfig)
}

func runAuthStatusCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox auth status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.Usage = func() { writeAuthStatusUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse auth status flags: %w", err)
	}
	configExplicit = configExplicit || flagWasSet(flags, configFlagName)
	if flags.NArg() != 0 {
		return fmt.Errorf("auth status received unexpected argument %q", flags.Arg(0))
	}

	return authStatus(ctx, stdout, home, configPath, configExplicit, defaultConfig)
}

func runHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		writeRootUsage(stdout)
		return nil
	}
	if len(args) == 1 && writeCommandUsage(args[0], stdout) {
		return nil
	}
	if len(args) == nestedCommandArgs && writeNestedCommandUsage(args[0], args[1], stdout) {
		return nil
	}
	return fmt.Errorf("help received unknown command %q", args[0])
}

func writeNestedCommandUsage(command string, nested string, writer io.Writer) bool {
	switch command {
	case authCommandName:
		return writeAuthCommandUsage(nested, writer)
	case showsCommandName:
		return writeShowsCommandUsage(nested, writer)
	case episodesCommandName:
		return writeEpisodesCommandUsage(nested, writer)
	case membersCommandName:
		return writeMembersCommandUsage(nested, writer)
	}
	return false
}

func writeShowsCommandUsage(command string, writer io.Writer) bool {
	switch command {
	case createCommandName:
		writeShowsCreateUsage(writer)
	case listCommandName:
		writeShowsListUsage(writer)
	case deleteCommandName:
		writeShowsDeleteUsage(writer)
	default:
		return false
	}
	return true
}

func writeEpisodesCommandUsage(command string, writer io.Writer) bool {
	switch command {
	case createCommandName:
		writeEpisodesCreateUsage(writer)
	case listCommandName:
		writeEpisodesListUsage(writer)
	default:
		return false
	}
	return true
}

func writeMembersCommandUsage(command string, writer io.Writer) bool {
	switch command {
	case listCommandName:
		writeMembersListUsage(writer)
	case inviteCommandName:
		writeMembersInviteUsage(writer)
	case roleCommandName:
		writeMembersRoleUsage(writer)
	case removeCommandName:
		writeMembersRemoveUsage(writer)
	default:
		return false
	}
	return true
}

func writeCommandUsage(command string, writer io.Writer) bool {
	switch command {
	case loginCommandName:
		writeLoginUsage(writer)
	case authCommandName:
		writeAuthUsage(writer)
	case importCommandName:
		writeImportUsage(writer)
	case showsCommandName:
		writeShowsUsage(writer)
	case episodesCommandName:
		writeEpisodesUsage(writer)
	case membersCommandName:
		writeMembersUsage(writer)
	default:
		return false
	}
	return true
}

func writeAuthCommandUsage(command string, writer io.Writer) bool {
	switch command {
	case statusCommandName:
		writeAuthStatusUsage(writer)
	default:
		return false
	}
	return true
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == name {
			found = true
		}
	})
	return found
}

func writeRootUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] <command>")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Commands:")
	_, _ = fmt.Fprintln(writer, "  login    Authorize this CLI with Listenbox")
	_, _ = fmt.Fprintln(writer, "  import   Import an RSS feed")
	_, _ = fmt.Fprintln(writer, "  auth     Manage CLI authorization")
	_, _ = fmt.Fprintln(writer, "  shows    Manage shows by slug")
	_, _ = fmt.Fprintln(writer, "  episodes Manage episodes")
	_, _ = fmt.Fprintln(writer, "  members  Manage team members")
	_, _ = fmt.Fprintln(writer, "  help     Show help")
}

func writeImportUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] import [--config PATH] [--slug SLUG] <source-url>",
	)
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Options:")
	_, _ = fmt.Fprintln(writer, "  --slug SLUG     Request a global show slug; omit for a server-generated slug")
	_, _ = fmt.Fprintln(writer, "  --config PATH   Path to CLI config file")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Output:")
	_, _ = fmt.Fprintln(
		writer,
		"  Progress and the management link are written to stderr; the final show slug is written to stdout, with the "+
			"trace ID when enabled.",
	)
}

func writeLoginUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] login [--config PATH]")
}

func writeAuthStatusUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] auth status [--config PATH]")
}

func writeAuthUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] auth <command>")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Commands:")
	_, _ = fmt.Fprintln(writer, "  status    Show current CLI authorization")
}

func writeShowsUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] shows <command>")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Commands:")
	_, _ = fmt.Fprintln(writer, "  list      List accessible show slugs")
	_, _ = fmt.Fprintln(writer, "  create    Create a show")
	_, _ = fmt.Fprintln(writer, "  delete    Permanently delete a show")
}

func writeShowsListUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] shows list [--config PATH]")
}

func writeShowsDeleteUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] shows delete --show SLUG --yes [--config PATH]")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Options:")
	_, _ = fmt.Fprintln(writer, "  --show SLUG    Global show slug (required)")
	_, _ = fmt.Fprintln(writer, "  --yes          Confirm permanent deletion (required)")
	_, _ = fmt.Fprintln(writer, "  --config PATH  Path to CLI config file")
}

func writeShowsCreateUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] shows create --title TITLE --slug SLUG "+
			"--type audio|video --language LANGUAGE [--artwork FILE] [--config PATH]",
	)
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Options:")
	_, _ = fmt.Fprintln(writer, "  --title TITLE  Show title (required)")
	_, _ = fmt.Fprintln(writer, "  --slug SLUG    Global show slug (required)")
	_, _ = fmt.Fprintln(writer, "  --type TYPE    Kind of podcast: audio or video (required)")
	_, _ = fmt.Fprintln(writer, "  --language CODE Podcast language, such as en or en-US (required)")
	_, _ = fmt.Fprintln(writer, "  --artwork FILE Optional canonical show artwork")
	_, _ = fmt.Fprintln(writer, "  --config PATH  Path to CLI config file")
}

func writeEpisodesUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] episodes <command>")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Commands:")
	_, _ = fmt.Fprintln(writer, "  list      List episode IDs newest first")
	_, _ = fmt.Fprintln(writer, "  create    Create a draft episode and upload its audio source")
	_, _ = fmt.Fprintln(writer, "  delete    Permanently delete an episode by ID")
}

func writeEpisodesDeleteUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] episodes delete --episode ID --yes [--config PATH]")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Options:")
	_, _ = fmt.Fprintln(writer, "  --episode ID   Episode ID (required)")
	_, _ = fmt.Fprintln(writer, "  --yes          Confirm permanent deletion (required)")
	_, _ = fmt.Fprintln(writer, "  --config PATH  Path to CLI config file")
}

func writeEpisodesListUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] episodes list --show SLUG [--limit N] [--config PATH]",
	)
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Options:")
	_, _ = fmt.Fprintln(writer, "  --show SLUG    Existing show slug (required)")
	_, _ = fmt.Fprintln(writer, "  --limit N      Episodes requested per page (1-500; default 100)")
	_, _ = fmt.Fprintln(writer, "  --config PATH  Path to CLI config file")
}

func writeEpisodesCreateUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] episodes create --show SLUG --title TITLE "+
			"[--description TEXT] --file PATH [--config PATH]",
	)
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Options:")
	_, _ = fmt.Fprintln(writer, "  --show SLUG         Existing show slug (required)")
	_, _ = fmt.Fprintln(writer, "  --title TITLE       Draft episode title (required)")
	_, _ = fmt.Fprintln(writer, "  --description TEXT  Optional draft description")
	_, _ = fmt.Fprintln(writer, "  --file PATH         Audio source file (required)")
	_, _ = fmt.Fprintln(writer, "  --config PATH       Path to CLI config file")
}

func writeMembersUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] members <command>")
	_, _ = fmt.Fprintln(writer)
	_, _ = fmt.Fprintln(writer, "Commands:")
	_, _ = fmt.Fprintln(writer, "  list      List members and pending invitations")
	_, _ = fmt.Fprintln(writer, "  invite    Invite an email address")
	_, _ = fmt.Fprintln(writer, "  role      Change a member role")
	_, _ = fmt.Fprintln(writer, "  remove    Remove a member")
}

func writeMembersListUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "Usage: listenbox [--config PATH] members list [--show SLUG] [--config PATH]")
}

func writeMembersInviteUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] members invite --email EMAIL --role read|write [--show SLUG] [--config PATH]",
	)
}

func writeMembersRoleUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] members role --member USER_ID --role read|write [--show SLUG] [--config PATH]",
	)
}

func writeMembersRemoveUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"Usage: listenbox [--config PATH] members remove --member USER_ID --yes [--show SLUG] [--config PATH]",
	)
}

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

const (
	emailFlagName  = "email"
	memberFlagName = "member"
	roleFlagName   = "role"
)

func runMembersCommand(
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
		writeMembersUsage(stderr)
		return errors.New("members command is required")
	}
	switch args[0] {
	case helpCommandName, shortHelpFlag, longHelpFlag:
		return runMembersHelp(args, stdout)
	case listCommandName:
		return runMembersList(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case inviteCommandName:
		return runMembersInvite(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case roleCommandName:
		return runMembersRole(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	case removeCommandName:
		return runMembersRemove(ctx, args[1:], stdout, stderr, home, configPath, configExplicit, defaultConfig)
	default:
		writeMembersUsage(stderr)
		return fmt.Errorf("unknown members command %q", args[0])
	}
}

func runMembersHelp(args []string, stdout io.Writer) error {
	if len(args) == 1 {
		writeMembersUsage(stdout)
		return nil
	}
	if len(args) != nestedCommandArgs || !writeNestedCommandUsage(membersCommandName, args[1], stdout) {
		return fmt.Errorf("members help received unknown command %q", args[1])
	}
	return nil
}

func runMembersList(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox members list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showSlug := ""
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&showSlug, showFlagName, "", "show slug; omit to list team members")
	flags.Usage = func() { writeMembersListUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse members list flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("members list received unexpected argument %q", flags.Arg(0))
	}
	client, config, err := teamManagementClient(
		home,
		configPath,
		configExplicit || flagWasSet(flags, configFlagName),
		defaultConfig,
	)
	if err != nil {
		return err
	}
	showSlug = strings.TrimSpace(showSlug)
	if showSlug != "" {
		return listShowMembers(ctx, client, config.apiOrigin, showSlug, stdout)
	}
	return listTeamMembers(ctx, client, config.apiOrigin, stdout)
}

func listShowMembers(
	ctx context.Context,
	client *publicapi.Client,
	apiOrigin string,
	showSlug string,
	stdout io.Writer,
) error {
	response, err := client.ListShowMembers(ctx, publicapi.ListShowMembersParams{ShowSlug: showSlug})
	if err != nil {
		return fmt.Errorf("list show members: %w", err)
	}
	if response == nil {
		return fmt.Errorf("list show members against %q returned no response", apiOrigin)
	}
	if response.Status200 == nil {
		return teamManagementResponseError(
			"list show members", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status404,
		)
	}
	if err := writeShowMembers(stdout, response.Status200.Members); err != nil {
		return err
	}
	return writePendingShowInvitations(stdout, response.Status200.Invitations)
}

func listTeamMembers(
	ctx context.Context,
	client *publicapi.Client,
	apiOrigin string,
	stdout io.Writer,
) error {
	response, err := client.ListTeamMembers(ctx)
	if err != nil {
		return fmt.Errorf("list members: %w", err)
	}
	if response == nil {
		return fmt.Errorf("list members against %q returned no response", apiOrigin)
	}
	if response.Status200 == nil {
		return teamManagementResponseError(
			"list members", response.StatusCode, response.Status400,
			response.Status401, response.Status403, false,
		)
	}
	if err := writeTeamMembers(stdout, response.Status200.Members); err != nil {
		return err
	}
	return writePendingTeamInvitations(stdout, response.Status200.Invitations)
}

func writeShowMembers(stdout io.Writer, members []publicapi.ShowMember) error {
	for _, member := range members {
		if _, err := fmt.Fprintf(
			stdout,
			"%s\t%s\t%s\t%s\n",
			member.AccessSource,
			member.Role,
			member.User.Email,
			member.User.Id,
		); err != nil {
			return fmt.Errorf("print show member %q: %w", member.User.Id, err)
		}
	}
	return nil
}

func writePendingShowInvitations(stdout io.Writer, invitations []publicapi.PendingShowInvitation) error {
	for _, invitation := range invitations {
		expires := time.UnixMilli(invitation.ExpiresAt).UTC().Format(time.RFC3339)
		if _, err := fmt.Fprintf(
			stdout,
			"pending\t%s\t%s\t%s\t%s\n",
			invitation.Role,
			invitation.Email,
			invitation.Id,
			expires,
		); err != nil {
			return fmt.Errorf("print show invitation %q: %w", invitation.Id, err)
		}
	}
	return nil
}

func writeTeamMembers(stdout io.Writer, members []publicapi.TeamMember) error {
	for _, member := range members {
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", member.Role, member.User.Email, member.User.Id); err != nil {
			return fmt.Errorf("print member %q: %w", member.User.Id, err)
		}
	}
	return nil
}

func writePendingTeamInvitations(stdout io.Writer, invitations []publicapi.PendingTeamInvitation) error {
	for _, invitation := range invitations {
		expires := time.UnixMilli(invitation.ExpiresAt).UTC().Format(time.RFC3339)
		if _, err := fmt.Fprintf(
			stdout,
			"pending\t%s\t%s\t%s\t%s\n",
			invitation.Role,
			invitation.Email,
			invitation.Id,
			expires,
		); err != nil {
			return fmt.Errorf("print invitation %q: %w", invitation.Id, err)
		}
	}
	return nil
}

func runMembersInvite(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox members invite", flag.ContinueOnError)
	flags.SetOutput(stderr)
	email, role, showSlug := "", "", ""
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&email, emailFlagName, "", "email address to invite (required)")
	flags.StringVar(&role, roleFlagName, "", "member role: read or write (required)")
	flags.StringVar(&showSlug, showFlagName, "", "show slug; omit to invite a team member")
	flags.Usage = func() { writeMembersInviteUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse members invite flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("members invite received unexpected argument %q", flags.Arg(0))
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return errors.New("members invite: --email is required")
	}
	if err := validateMemberRole(role, "members invite"); err != nil {
		return err
	}
	client, _, err := teamManagementClient(
		home,
		configPath,
		configExplicit || flagWasSet(flags, configFlagName),
		defaultConfig,
	)
	if err != nil {
		return err
	}
	showSlug = strings.TrimSpace(showSlug)
	if showSlug != "" {
		invitation, inviteErr := inviteShowMember(ctx, client, showSlug, email, role)
		if inviteErr != nil {
			return inviteErr
		}
		_, err = fmt.Fprintf(stdout, "Invited %s to %s as %s.\n", invitation.Email, showSlug, invitation.Role)
		return err
	}
	invitation, err := inviteTeamMember(ctx, client, email, role)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Invited %s as %s.\n", invitation.Email, invitation.Role)
	return err
}

func inviteShowMember(
	ctx context.Context,
	client *publicapi.Client,
	showSlug string,
	email string,
	role string,
) (publicapi.PendingShowInvitation, error) {
	response, err := client.CreateShowInvitation(ctx, publicapi.CreateShowInvitationParams{
		ShowSlug: showSlug,
		Body: publicapi.CreateShowInvitation{
			Email: email,
			Role:  publicapi.AssignableTeamRole(role),
		},
	})
	if err != nil {
		return publicapi.PendingShowInvitation{}, fmt.Errorf("invite show member %q: %w", email, err)
	}
	if response == nil {
		return publicapi.PendingShowInvitation{}, errors.New("invite show member returned no response")
	}
	if response.Status201 == nil {
		return publicapi.PendingShowInvitation{}, teamManagementResponseError(
			"invite show member", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status409,
		)
	}
	return *response.Status201, nil
}

func inviteTeamMember(
	ctx context.Context,
	client *publicapi.Client,
	email string,
	role string,
) (publicapi.PendingTeamInvitation, error) {
	response, err := client.CreateTeamInvitation(ctx, publicapi.CreateTeamInvitationParams{
		Body: publicapi.CreateTeamInvitation{
			Email: email,
			Role:  publicapi.AssignableTeamRole(role),
		},
	})
	if err != nil {
		return publicapi.PendingTeamInvitation{}, fmt.Errorf("invite member %q: %w", email, err)
	}
	if response == nil {
		return publicapi.PendingTeamInvitation{}, errors.New("invite member returned no response")
	}
	if response.Status201 == nil {
		return publicapi.PendingTeamInvitation{}, teamManagementResponseError(
			"invite member", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status409,
		)
	}
	return *response.Status201, nil
}

func runMembersRole(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox members role", flag.ContinueOnError)
	flags.SetOutput(stderr)
	memberID, role, showSlug := "", "", ""
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&memberID, memberFlagName, "", "member user ID (required)")
	flags.StringVar(&role, roleFlagName, "", "member role: read or write (required)")
	flags.StringVar(&showSlug, showFlagName, "", "show slug; omit to change a team member")
	flags.Usage = func() { writeMembersRoleUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse members role flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("members role received unexpected argument %q", flags.Arg(0))
	}
	if !strings.HasPrefix(memberID, "usr_") {
		return errors.New("members role: --member must be a user ID")
	}
	if err := validateMemberRole(role, "members role"); err != nil {
		return err
	}
	client, _, err := teamManagementClient(
		home,
		configPath,
		configExplicit || flagWasSet(flags, configFlagName),
		defaultConfig,
	)
	if err != nil {
		return err
	}
	showSlug = strings.TrimSpace(showSlug)
	if showSlug != "" {
		return updateShowMemberRole(ctx, client, showSlug, memberID, role, stdout)
	}
	return updateTeamMemberRole(ctx, client, memberID, role, stdout)
}

func updateShowMemberRole(
	ctx context.Context,
	client *publicapi.Client,
	showSlug string,
	memberID string,
	role string,
	stdout io.Writer,
) error {
	response, err := client.UpdateShowMemberRole(ctx, publicapi.UpdateShowMemberRoleParams{
		ShowSlug: showSlug,
		UserId:   memberID,
		Body: publicapi.UpdateShowMemberRole{
			Role: publicapi.AssignableTeamRole(role),
		},
	})
	if err != nil {
		return fmt.Errorf("change show member %q role: %w", memberID, err)
	}
	if response == nil {
		return errors.New("change show member role returned no response")
	}
	if !response.Status204 {
		return teamManagementResponseError(
			"change show member role", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status404,
		)
	}
	_, err = fmt.Fprintf(stdout, "Changed show member %s to %s.\n", memberID, role)
	return err
}

func updateTeamMemberRole(
	ctx context.Context,
	client *publicapi.Client,
	memberID string,
	role string,
	stdout io.Writer,
) error {
	response, err := client.UpdateTeamMemberRole(ctx, publicapi.UpdateTeamMemberRoleParams{
		UserId: memberID,
		Body: publicapi.UpdateTeamMemberRole{
			Role: publicapi.AssignableTeamRole(role),
		},
	})
	if err != nil {
		return fmt.Errorf("change member %q role: %w", memberID, err)
	}
	if response == nil {
		return errors.New("change member role returned no response")
	}
	if !response.Status204 {
		return teamManagementResponseError(
			"change member role", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status404,
		)
	}
	_, err = fmt.Fprintf(stdout, "Changed member %s to %s.\n", memberID, role)
	return err
}

func runMembersRemove(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	flags := flag.NewFlagSet("listenbox members remove", flag.ContinueOnError)
	flags.SetOutput(stderr)
	memberID, showSlug, confirmed := "", "", false
	flags.StringVar(&configPath, configFlagName, configPath, "path to CLI config file")
	flags.StringVar(&memberID, memberFlagName, "", "member user ID (required)")
	flags.BoolVar(&confirmed, yesFlagName, false, "confirm removal (required)")
	flags.StringVar(&showSlug, showFlagName, "", "show slug; omit to remove a team member")
	flags.Usage = func() { writeMembersRemoveUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse members remove flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("members remove received unexpected argument %q", flags.Arg(0))
	}
	if !strings.HasPrefix(memberID, "usr_") {
		return errors.New("members remove: --member must be a user ID")
	}
	if !confirmed {
		return errors.New("members remove: --yes is required")
	}
	client, _, err := teamManagementClient(
		home,
		configPath,
		configExplicit || flagWasSet(flags, configFlagName),
		defaultConfig,
	)
	if err != nil {
		return err
	}
	showSlug = strings.TrimSpace(showSlug)
	if showSlug != "" {
		return removeShowMember(ctx, client, showSlug, memberID, stdout)
	}
	return removeTeamMember(ctx, client, memberID, stdout)
}

func removeShowMember(
	ctx context.Context,
	client *publicapi.Client,
	showSlug string,
	memberID string,
	stdout io.Writer,
) error {
	response, err := client.RemoveShowMember(ctx, publicapi.RemoveShowMemberParams{
		ShowSlug: showSlug,
		UserId:   memberID,
	})
	if err != nil {
		return fmt.Errorf("remove show member %q: %w", memberID, err)
	}
	if response == nil {
		return errors.New("remove show member returned no response")
	}
	if !response.Status204 {
		return teamManagementResponseError(
			"remove show member", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status404,
		)
	}
	_, err = fmt.Fprintf(stdout, "Removed show member %s.\n", memberID)
	return err
}

func removeTeamMember(
	ctx context.Context,
	client *publicapi.Client,
	memberID string,
	stdout io.Writer,
) error {
	response, err := client.RemoveTeamMember(ctx, publicapi.RemoveTeamMemberParams{UserId: memberID})
	if err != nil {
		return fmt.Errorf("remove member %q: %w", memberID, err)
	}
	if response == nil {
		return errors.New("remove member returned no response")
	}
	if !response.Status204 {
		return teamManagementResponseError(
			"remove member", response.StatusCode, response.Status400,
			response.Status401, response.Status403, response.Status404,
		)
	}
	_, err = fmt.Fprintf(stdout, "Removed member %s.\n", memberID)
	return err
}

func teamManagementClient(
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) (*publicapi.Client, loadedCLIConfig, error) {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return nil, loadedCLIConfig{}, err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return nil, loadedCLIConfig{}, errors.New("team members: not logged in; run listenbox login")
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return nil, loadedCLIConfig{}, err
	}
	return client, config, nil
}

func validateMemberRole(role string, command string) error {
	if role != "read" && role != "write" {
		return fmt.Errorf("%s: --role must be read or write", command)
	}
	return nil
}

func teamManagementResponseError(
	action string,
	status int,
	validation *publicapi.ValidationErr,
	unauthorized bool,
	forbidden bool,
	conflictOrNotFound bool,
) error {
	switch {
	case validation != nil:
		return fmt.Errorf("%s: %s", action, validation.Message)
	case unauthorized:
		return fmt.Errorf("%s: authentication failed; run listenbox login", action)
	case forbidden:
		return fmt.Errorf("%s: team owner permission is required", action)
	case conflictOrNotFound:
		return fmt.Errorf("%s: target is unavailable", action)
	default:
		return fmt.Errorf("%s returned HTTP status %d", action, status)
	}
}

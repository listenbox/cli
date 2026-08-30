package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

const (
	titleFlagArgument    = "--" + titleFlagName
	showFlagArgument     = "--" + showFlagName
	slugFlagArgument     = "--" + slugFlagName
	typeFlagArgument     = "--" + typeFlagName
	languageFlagArgument = "--" + languageFlagName
	testAudioPodcastType = "audio"
	testVideoPodcastType = "video"
	testShowsCreateTitle = "Daily"
	testShowsCreateSlug  = "daily"
	testCLIShowSlug      = "cli-show"
	testShowIDField      = "show_id"
	testEpisodeTitle     = "Title"
	testEpisodeSource    = "source.mp3"
)

func TestHelpOnlyDocumentsSupportedCommands(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{helpCommandName},
		&stdout,
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout.String(), "login"))
	assert.Assert(t, strings.Contains(stdout.String(), "auth"))
	assert.Assert(t, !strings.Contains(stdout.String(), "whoami"))
	assert.Assert(t, strings.Contains(stdout.String(), "shows"))
	assert.Assert(t, bytes.Contains(stdout.Bytes(), []byte("episodes")))
}

func TestAuthHelpDocumentsSupportedCommands(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{helpCommandName, authCommandName},
		&stdout,
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(stdout.String(), "login"))
	assert.Assert(t, strings.Contains(stdout.String(), "status"))
	assert.Assert(t, !strings.Contains(stdout.String(), "whoami"))
}

func TestLoginHelpDocumentsTopLevelCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{helpCommandName, loginCommandName},
		&stdout,
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout.String(), "listenbox [--config PATH] login"))
	assert.Assert(t, !strings.Contains(stdout.String(), "auth login"))
}

func TestUnknownRootWhoamiFailsCleanly(t *testing.T) {
	t.Parallel()

	err := runCLIWithHome(
		context.Background(),
		[]string{"whoami"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, fmt.Sprintf("unknown command %q", "whoami"))
}

func TestNestedAuthLoginFailsCleanly(t *testing.T) {
	t.Parallel()

	err := runCLIWithHome(
		context.Background(),
		[]string{authCommandName, loginCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, fmt.Sprintf("unknown auth command %q", loginCommandName))
}

func TestShowsCreateRequiresTitleSlugAndTypeFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "all missing",
			args: []string{showsCommandName, createCommandName},
			want: titleFlagArgument,
		},
		{
			name: "title missing",
			args: []string{
				showsCommandName,
				createCommandName,
				slugFlagArgument,
				testShowsCreateSlug,
				typeFlagArgument,
				testAudioPodcastType,
			},
			want: titleFlagArgument,
		},
		{
			name: "slug missing",
			args: []string{
				showsCommandName,
				createCommandName,
				titleFlagArgument,
				testShowsCreateTitle,
				typeFlagArgument,
				testAudioPodcastType,
			},
			want: slugFlagArgument,
		},
		{
			name: "type missing",
			args: []string{
				showsCommandName,
				createCommandName,
				titleFlagArgument,
				testShowsCreateTitle,
				slugFlagArgument,
				testShowsCreateSlug,
			},
			want: "--type is required",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assertCLIErrorContains(t, testCase.args, testCase.want)
		})
	}
}

func TestShowsCreateReportsAllMissingRequiredFlags(t *testing.T) {
	t.Parallel()

	err := runCLIWithHome(
		context.Background(),
		[]string{showsCommandName, createCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.Equal(t, err.Error(), "shows create: invalid arguments\n"+
		"  --title is required\n"+
		"  --slug is required\n"+
		"  --type is required\n"+
		"  --language is required")
}

func assertCLIErrorContains(t *testing.T, args []string, want string) {
	t.Helper()
	err := runCLIWithHome(
		context.Background(),
		args,
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, want)
}

func TestShowsCreateRejectsInvalidPodcastType(t *testing.T) {
	t.Parallel()

	err := runCLIWithHome(
		context.Background(),
		[]string{
			showsCommandName,
			createCommandName,
			titleFlagArgument,
			testShowsCreateTitle,
			slugFlagArgument,
			testShowsCreateSlug,
			typeFlagArgument,
			"livestream",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, "--type must be audio or video")
}

func TestShowsCreateHelpDocumentsRequiredPodcastType(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{helpCommandName, showsCommandName, createCommandName},
		&stdout,
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout.String(), "--title TITLE"))
	assert.Assert(t, strings.Contains(stdout.String(), "--slug SLUG"))
	assert.Assert(t, strings.Contains(stdout.String(), "--type TYPE"))
	assert.Assert(t, strings.Contains(stdout.String(), "Kind of podcast: audio or video (required)"))
	assert.Assert(t, !strings.Contains(strings.ToLower(stdout.String()), "source type"))
	assert.Assert(t, !strings.Contains(strings.ToLower(stdout.String()), "source kind"))
}

func TestShowsListHelpDocumentsCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{helpCommandName, showsCommandName, listCommandName},
		&stdout,
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout.String(), "shows list"))
}

func TestUnknownCommandFailsCleanly(t *testing.T) {
	t.Parallel()
	err := runCLIWithHome(
		context.Background(),
		[]string{"upload"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, `unknown command "upload"`)
}

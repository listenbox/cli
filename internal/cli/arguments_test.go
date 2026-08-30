//nolint:lll // Fixtures keep each invocation together.
package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
)

//nolint:funlen // Validation table covers each required and optional field boundary.
func TestEpisodesCreateArgumentValidation(t *testing.T) {
	t.Parallel()

	tests := make([]struct {
		name string
		args episodesCreateArguments
		want string
	}, 0, 8)
	tests = append(tests,
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "show missing", args: episodesCreateArguments{Title: testEpisodeTitle, File: testEpisodeSource}, want: "--show is required"},
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "title missing", args: episodesCreateArguments{Show: testShowsCreateSlug, File: testEpisodeSource}, want: "--title is required"},
	)
	tests = append(tests,
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "file missing", args: episodesCreateArguments{Show: testShowsCreateSlug, Title: testEpisodeTitle}, want: "--file is required"},
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "show whitespace", args: episodesCreateArguments{Show: "  ", Title: testEpisodeTitle, File: testEpisodeSource}, want: "--show is required"},
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "title whitespace", args: episodesCreateArguments{Show: testShowsCreateSlug, Title: "  ", File: testEpisodeSource}, want: "--title is required"},
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "file whitespace", args: episodesCreateArguments{Show: testShowsCreateSlug, Title: testEpisodeTitle, File: "  "}, want: "--file is required"},
		struct {
			name string
			args episodesCreateArguments
			want string
		}{name: "show invalid", args: episodesCreateArguments{Show: "Not A Slug", Title: testEpisodeTitle, File: testEpisodeSource}, want: "--show must be a valid show slug"},
	)
	blank := "  "
	tests = append(tests, struct {
		name string
		args episodesCreateArguments
		want string
	}{name: "description blank when present", args: episodesCreateArguments{
		Show: testShowsCreateSlug, Title: testEpisodeTitle, Description: &blank, File: testEpisodeSource,
	}, want: "--description must not be blank when provided"})
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := validateEpisodesCreateArguments(&testCase.args)
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestEpisodesCreateReportsAllMissingArgumentsDeterministically(t *testing.T) {
	t.Parallel()
	want := "episodes create: invalid arguments\n" +
		"  --show is required\n" +
		"  --title is required\n" +
		"  --file is required"
	for range 20 {
		args := episodesCreateArguments{}
		assert.Equal(t, validateEpisodesCreateArguments(&args).Error(), want)
	}
}

func TestEpisodesCreateDescriptionMayBeOmitted(t *testing.T) {
	t.Parallel()
	args := episodesCreateArguments{Show: testShowsCreateSlug, Title: testEpisodeTitle, File: testEpisodeSource}
	assert.NilError(t, validateEpisodesCreateArguments(&args))
}

func TestShowsCreateArgumentOrderIsDeterministic(t *testing.T) {
	t.Parallel()
	want := "shows create: invalid arguments\n" +
		"  --title is required\n" +
		"  --slug is required\n" +
		"  --type is required\n" +
		"  --language is required"
	for range 20 {
		args := showsCreateArguments{}
		assert.Equal(t, validateShowsCreateArguments(&args).Error(), want)
	}
}

func TestEpisodesCreateMissingFlagValueIsControlled(t *testing.T) {
	t.Parallel()
	err := runEpisodesCreateCommand(
		context.Background(), []string{"--show"}, &bytes.Buffer{}, &bytes.Buffer{},
		t.TempDir(), "", false, loadedCLIConfig{},
	)
	assert.ErrorContains(t, err, "flag needs an argument: -show")
}

func TestEpisodesCreateValidationMakesNoHTTPRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	home := t.TempDir()
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	err := runEpisodesCreateCommand(
		context.Background(), []string{"--show", testShowsCreateSlug}, &bytes.Buffer{}, &bytes.Buffer{},
		home, "", false,
		loadedCLIConfig{apiOrigin: server.URL, dashboardOrigin: server.URL},
	)
	assert.ErrorContains(t, err, "--title is required")
	assert.ErrorContains(t, err, "--file is required")
	assert.Equal(t, calls.Load(), int64(0))
}

func TestShowsCreateRejectsWhitespaceAndInvalidSlug(t *testing.T) {
	t.Parallel()
	arguments := showsCreateArguments{Title: strings.Repeat(" ", 3), Slug: "Bad Slug", Type: "audio", Language: "en"}
	err := validateShowsCreateArguments(&arguments)
	assert.ErrorContains(t, err, "--title is required")
	assert.ErrorContains(t, err, "--slug must use lowercase letters, numbers, and single hyphens")
}

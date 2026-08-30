package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"
)

func TestEpisodesListAutoDrainsPagesWithLimit(t *testing.T) {
	t.Parallel()

	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestNumber++
		assert.Equal(t, request.Method, http.MethodGet)
		assert.Equal(t, request.URL.Path, "/s/shows/cli-show/episodes")
		assert.Equal(t, request.Header.Get("Authorization"), "Bearer "+testCLIAPIKey)
		assert.Equal(t, request.URL.Query().Get("limit"), "2")
		writer.Header().Set("Content-Type", "application/json")
		switch requestNumber {
		case 1:
			assert.Equal(t, request.URL.Query().Get("cursor"), "")
			writeEpisodeListTestPage(t, writer, []string{"newest", "middle"}, "cursor-1")
		case 2:
			assert.Equal(t, request.URL.Query().Get("cursor"), "cursor-1")
			writeEpisodeListTestPage(t, writer, []string{"oldest"}, "")
		default:
			http.Error(writer, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{
			configFlagArgument, configPath, episodesCommandName, listCommandName,
			showFlagArgument, testCLIShowSlug, "--limit", "2",
		},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.NilError(t, err)
	assert.Equal(t, requestNumber, 2)
	assert.Equal(t, stdout.String(), "ep_newest\nep_middle\nep_oldest\n")
}

func TestEpisodesListOmitsOptionalLimitAndHandlesEmptyShow(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, limitPresent := request.URL.Query()["limit"]
		assert.Assert(t, !limitPresent, "limit omitted")
		writer.Header().Set("Content-Type", "application/json")
		writeEpisodeListTestPage(t, writer, []string{}, "")
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, episodesCommandName, listCommandName, showFlagArgument, testCLIShowSlug},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "")
}

func TestEpisodesListRejectsRepeatedCursorAfterStreamingPages(t *testing.T) {
	t.Parallel()

	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requestNumber++
		writer.Header().Set("Content-Type", "application/json")
		writeEpisodeListTestPage(t, writer, []string{fmt.Sprintf("episode-%d", requestNumber)}, "repeat")
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, episodesCommandName, listCommandName, showFlagArgument, testCLIShowSlug},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.ErrorContains(t, err, "returned repeated cursor")
	assert.Equal(t, stdout.String(), "ep_episode-1\nep_episode-2\n")
}

func TestEpisodesListPreservesStreamedOutputWhenLaterPageFails(t *testing.T) {
	t.Parallel()

	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requestNumber++
		if requestNumber == 1 {
			writer.Header().Set("Content-Type", "application/json")
			writeEpisodeListTestPage(t, writer, []string{"already-streamed"}, "next")
			return
		}
		http.Error(writer, "later failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, episodesCommandName, listCommandName, showFlagArgument, testCLIShowSlug},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.ErrorContains(t, err, "later failure")
	assert.Equal(t, stdout.String(), "ep_already-streamed\n")
}

func writeEpisodeListTestPage(t *testing.T, writer http.ResponseWriter, slugs []string, nextCursor string) {
	t.Helper()
	episodes := make([]map[string]any, 0, len(slugs))
	for _, slug := range slugs {
		episodes = append(episodes, map[string]any{
			"id": "ep_" + slug, testShowIDField: "shw_test", "title": slug,
			"slug": slug, statusCommandName: "draft", "enclosures": []any{},
		})
	}
	page := map[string]any{"episodes": episodes}
	if nextCursor != "" {
		page["next_cursor"] = nextCursor
	}
	assert.NilError(t, json.NewEncoder(writer).Encode(page))
}

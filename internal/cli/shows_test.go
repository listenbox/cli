package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"gotest.tools/v3/assert"
)

//nolint:funlen // Complete CLI request and response contract stays visible in one test.
func TestShowsCreateSendsRequiredPodcastType(t *testing.T) {
	t.Parallel()

	for _, podcastType := range []string{testAudioPodcastType, testVideoPodcastType} {
		t.Run(podcastType, func(t *testing.T) {
			t.Parallel()

			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assert.Equal(t, request.URL.Path, "/s/shows")
				assert.Equal(t, request.Header.Get("Authorization"), "Bearer "+testCLIAPIKey)
				var body map[string]any
				assert.NilError(t, json.NewDecoder(request.Body).Decode(&body))
				requests <- body
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusCreated)
				_, err := fmt.Fprintf(
					writer,
					`{"id":"shw_0123456789abcdef","team_id":"team_0123456789abcdef",`+
						`"title":"CLI Show","slug":"`+testCLIShowSlug+`","source_kind":%q,"language":"en"}`,
					podcastType,
				)
				assert.NilError(t, err)
			}))
			defer server.Close()

			home := t.TempDir()
			configPath := writeTestCLIConfig(t, home, server.URL, "https://dashboard.test:9443")
			assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
			var stdout bytes.Buffer
			err := runCLIWithHome(
				context.Background(),
				[]string{
					configFlagArgument,
					configPath,
					showsCommandName,
					createCommandName,
					titleFlagArgument,
					"CLI Show",
					slugFlagArgument,
					testCLIShowSlug,
					typeFlagArgument,
					podcastType,
					languageFlagArgument,
					"en",
				},
				&stdout,
				&bytes.Buffer{},
				home,
			)
			assert.NilError(t, err)
			wantOutput := "Created show \"cli-show\"\n" +
				"Open in Listenbox: https://dashboard.test:9443/" +
				"team_0123456789abcdef/shows/shw_0123456789abcdef\n"
			assert.Equal(t, stdout.String(), wantOutput)
			body := <-requests
			showID, ok := body["id"].(string)
			assert.Assert(t, ok)
			assert.Assert(t, regexp.MustCompile(`^shw_[a-f0-9]{16}$`).MatchString(showID))
			assert.Equal(t, body["title"], "CLI Show")
			assert.Equal(t, body["slug"], testCLIShowSlug)
			assert.Equal(t, body["source_kind"], podcastType)
			assert.Equal(t, body["language"], "en")
		})
	}
}

func TestShowsListPrintsAccessibleSlugs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, request.Method, http.MethodGet)
		assert.Equal(t, request.URL.Path, "/s/shows")
		assert.Equal(t, request.Header.Get("Authorization"), "Bearer "+testCLIAPIKey)
		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(
			writer,
			`[{"id":"shw_second","team_id":"team_test","title":"Second","slug":"second","source_kind":"video","language":"en"},`+
				`{"id":"shw_first","team_id":"team_test","title":"First","slug":"first","source_kind":"audio","language":"en"}]`,
		)
		assert.NilError(t, err)
	}))
	defer server.Close()

	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL, "https://dashboard.test")
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, showsCommandName, listCommandName},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "second\nfirst\n")
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
	"gotest.tools/v3/assert"
)

const (
	//nolint:gosec // Synthetic credential used only by local test servers.
	testCLIAPIKey                 = "apk_cli.AAAAAAAAAAAAAAAAAAAAAA"
	testRSSURL                    = "https://feeds.test/rss"
	testMalformedImportCompletion = "malformed completed terminal event"
)

//nolint:funlen // Covers all supported help entry points and positional errors together.
func TestImportHelpAndArgumentParsing(t *testing.T) {
	t.Parallel()

	t.Run("root lists import", func(t *testing.T) {
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
		assert.Assert(t, strings.Contains(stdout.String(), "import   Import an RSS feed"))
	})

	for _, args := range [][]string{
		{helpCommandName, importCommandName},
		{importCommandName, longHelpFlag},
		{importCommandName, shortHelpFlag},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := runCLIWithHome(context.Background(), args, &stdout, &stderr, t.TempDir())
			assert.NilError(t, err)
			output := stdout.String() + stderr.String()
			assert.Assert(t, strings.Contains(output,
				"Usage: listenbox [--config PATH] import [--config PATH] [--slug SLUG] <source-url>"))
			assert.Assert(t, strings.Contains(output, "--slug SLUG"))
			assert.Assert(t, strings.Contains(output, "--config PATH"))
			assert.Assert(t, strings.Contains(
				output,
				"Progress and the management link are written to stderr",
			))
			assert.Assert(t, strings.Contains(output, "final show slug is written to stdout"))
		})
	}

	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing URL", args: []string{importCommandName}, want: "requires exactly one source URL"},
		{
			name: "extra URL",
			args: []string{importCommandName, "https://one.test/rss", "https://two.test/rss"},
			want: `unexpected argument "https://two.test/rss"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			err := runCLIWithHome(
				context.Background(),
				testCase.args,
				&stdout,
				&bytes.Buffer{},
				t.TempDir(),
			)
			assert.ErrorContains(t, err, testCase.want)
			assert.Equal(t, stdout.String(), "")
		})
	}
}

//nolint:funlen // Covers exact request and split stdout/stderr contracts for optional slug cases.
func TestImportRequestAuthOptionalSlugAndExactOutput(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name           string
		args           []string
		wantSlug       string
		wantSlugInBody bool
	}{
		{
			name: "omitted slug",
			args: []string{"https://feeds.test/podcast.xml"},
		},
		{
			name:           "explicit slug",
			args:           []string{slugFlagArgument, "requested-show", "https://feeds.test/podcast.xml"},
			wantSlug:       "requested-show",
			wantSlugInBody: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan importTestRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					var body map[string]any
					decodeErr := json.NewDecoder(request.Body).Decode(&body)
					requests <- importTestRequest{
						authorization: request.Header.Get("Authorization"),
						body:          body,
						decodeErr:     decodeErr,
					}
					writeTestImportCreation(writer)
					return
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				writeTestSSEEvent(writer, map[string]any{testEventTypeField: importProgressEventType, "percent": 10})
				writeTestSSEEvent(writer, map[string]any{testEventTypeField: importProgressEventType, "percent": 10})
				writeTestSSEEvent(writer, map[string]any{
					testEventTypeField: importTerminalEventType,
					statusCommandName:  importCompletedStatus,
					"feed_id":          "lb_server_feed",
					testTeamIDField:    "team_0123456789abcdef",
					testShowIDField:    "shw_0123456789abcdef",
					"show_slug":        "actual-server-show",
				})
			}))
			defer server.Close()

			stdout, stderr, err := runImportAgainstServer(
				context.Background(),
				t,
				server.URL,
				testCase.args,
				"https://dashboard.test:9443",
			)
			assert.NilError(t, err)
			assert.Equal(t, stdout, "actual-server-show\n")
			assert.Equal(
				t,
				stderr,
				"Import progress: 10%\nImport progress: 100%\n"+
					"Open in Listenbox: https://dashboard.test:9443/team_0123456789abcdef/"+
					"shows/shw_0123456789abcdef\n",
			)
			request := <-requests
			assert.NilError(t, request.decodeErr)
			assert.Equal(t, request.authorization, "Bearer "+testCLIAPIKey)
			assert.Equal(t, request.body["source_url"], "https://feeds.test/podcast.xml")
			slug, hasSlug := request.body["slug"]
			assert.Equal(t, hasSlug, testCase.wantSlugInBody)
			if hasSlug {
				assert.Equal(t, slug, testCase.wantSlug)
			}
		})
	}
}

func TestImportAcceptsCommandLocalConfigFlag(t *testing.T) {
	t.Parallel()

	server := importCompletedTestServer("local-config-show")
	defer server.Close()
	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, server.URL)
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{importCommandName, "--config", configPath, testRSSURL},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "local-config-show\n")
}

func TestImportHTTPStatusMappingsLeaveStdoutEmpty(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "bad request",
			status: http.StatusBadRequest,
			body:   `{"message":"invalid RSS URL","errors":[]}`,
			want:   "invalid RSS URL",
		},
		{
			name:   "entitlement required",
			status: http.StatusPaymentRequired,
			body: `{"code":"rss_import_entitlement_required","media_kind":"video",` +
				`"current_entitlement":"audio","required_entitlement":"video_hd",` +
				`"pricing_url":"https://listenbox.app/pricing"}`,
			want: "video import requires the video_hd entitlement (current: audio); upgrade at " +
				"https://listenbox.app/pricing and retry",
		},
		{name: "unauthorized", status: http.StatusUnauthorized, want: "authentication failed"},
		{name: "forbidden", status: http.StatusForbidden, want: "lacks show:create scope"},
		{name: "conflict", status: http.StatusConflict, want: "requested slug \"taken\" conflicts"},
		{name: "unexpected", status: http.StatusTeapot, body: "unexpected", want: "HTTP status 418"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if testCase.body != "" {
					writer.Header().Set("Content-Type", "application/json")
				}
				writer.WriteHeader(testCase.status)
				_, _ = io.WriteString(writer, testCase.body)
			}))
			defer server.Close()
			stdout, _, err := runImportAgainstServer(
				context.Background(),
				t,
				server.URL,
				[]string{"--slug", "taken", testRSSURL},
			)
			assert.ErrorContains(t, err, testCase.want)
			assert.Equal(t, stdout, "")
		})
	}
}

//nolint:funlen // Table enumerates malformed and failed stream terminal contracts.
func TestImportStreamFailuresLeaveStdoutEmpty(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		events []string
		want   string
	}{
		{
			name: "failed terminal",
			events: []string{
				`{"type":"terminal","status":"failed","error_code":"feed_fetch_failed","error_message":"boom"}`,
			},
			want: "failed (feed_fetch_failed): boom",
		},
		{
			name:   "premature EOF",
			events: []string{`{"type":"progress","percent":30}`},
			want:   "ended before a terminal event",
		},
		{
			name:   "empty variant",
			events: []string{`{}`},
			want:   "unsupported PublicRSSImportEvent discriminator",
		},
		{
			name: "missing feed id",
			events: []string{
				`{"type":"terminal","status":"completed","team_id":"team_valid",` +
					`"show_id":"shw_valid","show_slug":"show"}`,
			},
			want: testMalformedImportCompletion,
		},
		{
			name: "missing show slug",
			events: []string{
				`{"type":"terminal","status":"completed","feed_id":"lb_feed",` +
					`"team_id":"team_valid","show_id":"shw_valid"}`,
			},
			want: testMalformedImportCompletion,
		},
		{
			name: "missing team id",
			events: []string{
				`{"type":"terminal","status":"completed","feed_id":"lb_feed",` +
					`"show_id":"shw_valid","show_slug":"show"}`,
			},
			want: "invalid team_id",
		},
		{
			name: "missing show id",
			events: []string{
				`{"type":"terminal","status":"completed","feed_id":"lb_feed",` +
					`"team_id":"team_valid","show_slug":"show"}`,
			},
			want: "invalid show_id",
		},
		{
			name: "unsafe team id",
			events: []string{
				`{"type":"terminal","status":"completed","feed_id":"lb_feed",` +
					`"team_id":"team_bad/segment","show_id":"shw_valid","show_slug":"show"}`,
			},
			want: "invalid team_id",
		},
		{
			name: "unsafe show slug",
			events: []string{
				`{"type":"terminal","status":"completed","feed_id":"lb_feed",` +
					`"team_id":"team_valid","show_id":"shw_valid","show_slug":"show\nextra"}`,
			},
			want: testMalformedImportCompletion,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var streamServed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					writeTestImportCreation(writer)
					return
				}
				if streamServed.Swap(true) {
					writer.WriteHeader(http.StatusNoContent)
					return
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				for _, event := range testCase.events {
					_, _ = io.WriteString(writer, "data: "+event+"\n\n")
				}
			}))
			defer server.Close()
			stdout, _, err := runImportAgainstServer(
				context.Background(),
				t,
				server.URL,
				[]string{testRSSURL},
			)
			assert.ErrorContains(t, err, testCase.want)
			assert.Equal(t, stdout, "")
		})
	}
}

func TestImportStreamCancellationFails(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := publicapi.NewSSEStream[publicapi.PublicRSSImportEvent](
		io.NopCloser(strings.NewReader("")),
	)
	var stdout bytes.Buffer
	err := consumeImportRSSStream(
		ctx,
		&stdout,
		&bytes.Buffer{},
		testRSSURL,
		"https://web.listenbox.app",
		stream,
	)
	assert.ErrorContains(t, err, "context canceled")
	assert.Equal(t, stdout.String(), "")
}

func TestImportProgressTTYAndNonTTY(t *testing.T) {
	t.Parallel()

	var plain bytes.Buffer
	plainProgress := newImportProgress(&plain, false)
	assert.NilError(t, plainProgress.update(25))
	assert.NilError(t, plainProgress.update(25))
	assert.NilError(t, plainProgress.complete())
	assert.Equal(t, plain.String(), "Import progress: 25%\nImport progress: 100%\n")

	var tty bytes.Buffer
	ttyProgress := newImportProgress(&tty, true)
	assert.NilError(t, ttyProgress.update(25))
	assert.NilError(t, ttyProgress.update(25))
	assert.NilError(t, ttyProgress.complete())
	assert.Equal(t, strings.Count(tty.String(), " 25%"), 1)
	assert.Equal(t, strings.Count(tty.String(), "100%"), 1)
	assert.Assert(t, strings.Contains(tty.String(), "\rImporting ["))
	assert.Assert(t, strings.HasSuffix(tty.String(), "\n"))
}

type importTestRequest struct {
	authorization string
	body          map[string]any
	decodeErr     error
}

func runImportAgainstServer(
	ctx context.Context,
	t *testing.T,
	apiOrigin string,
	importArgs []string,
	dashboardOrigins ...string,
) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	configPath := writeTestCLIConfig(t, home, apiOrigin, dashboardOrigins...)
	assert.NilError(t, writeStoredAuth(defaultAuthPath(home), storedAuth{APIKey: testCLIAPIKey}))
	args := make([]string, 0, 3+len(importArgs))
	args = append(args, configFlagArgument, configPath, importCommandName)
	args = append(args, importArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLIWithHome(ctx, args, &stdout, &stderr, home)
	return stdout.String(), stderr.String(), err
}

func importCompletedTestServer(showSlug string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			writeTestImportCreation(writer)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writeTestSSEEvent(writer, map[string]any{
			testEventTypeField: importTerminalEventType,
			"status":           "completed",
			"feed_id":          "lb_feed",
			testTeamIDField:    "team_0123456789abcdef",
			"show_id":          "shw_0123456789abcdef",
			"show_slug":        showSlug,
		})
	}))
}

func writeTestImportCreation(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Trace-Id", "0123456789abcdef0123456789abcdef")
	writer.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(writer).Encode(map[string]string{
		"import_run_id": "imp_0123456789abcdef0123456789abcdef",
	}); err != nil {
		panic(err)
	}
}

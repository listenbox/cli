package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
	"gotest.tools/v3/assert"
)

func TestAuthStatusPrintsStoredAuthorizationIdentity(t *testing.T) {
	t.Parallel()

	credential := "apk_test." + t.Name()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, request.URL.Path, "/s/whoami")
		assert.Equal(t, request.Header.Get("Authorization"), "Bearer "+credential)
		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(
			writer,
			`{"email":"john@example.com","id":"apk_test","name":"John",`+
				`"team_id":"lb_team_test","scopes":["show:create","show:update"]}`,
		)
		assert.NilError(t, err)
	}))
	defer server.Close()

	home := t.TempDir()
	writeAuthStatusTestAuth(t, home, credential)
	configPath := writeTestCLIConfig(t, home, server.URL)
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, authCommandName, statusCommandName},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.NilError(t, err)
	assert.Equal(
		t,
		stdout.String(),
		"Logged in as John <john@example.com>\n",
	)
}

func TestAuthStatusPrintsEmailWhenNameIsAbsent(t *testing.T) {
	t.Parallel()

	credential := "apk_test." + t.Name()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprint(
			writer,
			`{"email":"john@example.com","id":"apk_test","team_id":"lb_team_test","scopes":["show:create"]}`,
		)
		assert.NilError(t, err)
	}))
	defer server.Close()

	home := t.TempDir()
	writeAuthStatusTestAuth(t, home, credential)
	configPath := writeTestCLIConfig(t, home, server.URL)
	var stdout bytes.Buffer
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, authCommandName, statusCommandName},
		&stdout,
		&bytes.Buffer{},
		home,
	)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "Logged in as john@example.com\n")
}

func TestPrintAuthStatusRejectsMissingEmail(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := printAuthStatus(&stdout, publicapi.APIKeyWhoami{})
	assert.ErrorContains(t, err, "user email")
	assert.Equal(t, stdout.String(), "")
}

func TestAuthStatusRequiresStoredAuthorization(t *testing.T) {
	t.Parallel()

	err := runCLIWithHome(
		context.Background(),
		[]string{authCommandName, statusCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		t.TempDir(),
	)
	assert.ErrorContains(t, err, "not logged in; run listenbox login")
}

func TestAuthStatusReportsRejectedStoredAuthorization(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	home := t.TempDir()
	writeAuthStatusTestAuth(t, home, "apk_test.expired-secret")
	configPath := writeTestCLIConfig(t, home, server.URL)
	err := runCLIWithHome(
		context.Background(),
		[]string{configFlagArgument, configPath, authCommandName, statusCommandName},
		&bytes.Buffer{},
		&bytes.Buffer{},
		home,
	)
	assert.ErrorContains(t, err, "authentication failed; run listenbox login")
}

func writeAuthStatusTestAuth(t *testing.T, home string, credential string) {
	t.Helper()
	directory := filepath.Dir(defaultAuthPath(home))
	assert.NilError(t, os.MkdirAll(directory, authDirectoryMode))
	assert.NilError(
		t,
		os.WriteFile(defaultAuthPath(home), []byte(fmt.Sprintf(`{"api_key":%q}`, credential)), authFileMode),
	)
}

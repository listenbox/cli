package main

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLoadCLIConfigUsesEmbeddedDefaultWhenUserConfigIsMissing(t *testing.T) {
	t.Parallel()
	defaultConfig, err := loadEmbeddedCLIConfig()
	assert.NilError(t, err)
	config, err := loadCLIConfig("", false, t.TempDir(), defaultConfig)
	assert.NilError(t, err)
	assert.Equal(t, config.apiOrigin, defaultConfig.apiOrigin)
	assert.Equal(t, config.dashboardOrigin, defaultConfig.dashboardOrigin)
}

func TestLoadCLIConfigReadsImplicitUserConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ".config", "listenbox", "config.yaml")
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	assert.NilError(t, os.WriteFile(path, []byte(
		"api_origin: http://EXAMPLE.com:80/\n"+
			"dashboard_origin: https://WEB.example.com:443/\n"+
			"print_trace_ids: true\n",
	), 0o600))
	config, err := loadCLIConfig("", false, home, loadedCLIConfig{})
	assert.NilError(t, err)
	assert.Equal(t, config.apiOrigin, "http://example.com")
	assert.Equal(t, config.dashboardOrigin, "https://web.example.com")
	assert.Equal(t, config.printTraceIDs, true)
}

func TestLoadCLIConfigRejectsExplicitFailures(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	invalidPath := filepath.Join(home, "invalid.yaml")
	assert.NilError(t, os.WriteFile(invalidPath, []byte(
		"api_origin: https://example.com/path\n"+
			"dashboard_origin: https://example.com\n"+
			"print_trace_ids: false\n",
	), 0o600))

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "empty", path: "", want: "explicit config path"},
		{name: "missing", path: filepath.Join(home, "missing.yaml"), want: "load CLI config"},
		{name: "unreadable shape", path: home, want: "load CLI config"},
		{name: "invalid origin", path: invalidPath, want: "must not include a path"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadCLIConfig(testCase.path, true, home, loadedCLIConfig{})
			assert.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestNormalizeAPIOriginRejectsNonOrigins(t *testing.T) {
	t.Parallel()
	values := []string{
		"",
		"ftp://example.com",
		"https://user@example.com",
		"https://example.com?query=yes",
		"https://example.com#fragment",
	}
	for _, value := range values {
		_, err := normalizeAPIOrigin(value)
		assert.Assert(t, err != nil, value)
	}
}

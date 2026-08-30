package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	cliconfig "github.com/listenbox/listenbox-cli/internal/cliconfig"
)

const (
	httpScheme  = "http"
	httpsScheme = "https"
)

type loadedCLIConfig struct {
	apiOrigin       string
	dashboardOrigin string
	printTraceIDs   bool
}

func loadCLIConfig(
	path string,
	explicit bool,
	home string,
	defaultConfig loadedCLIConfig,
) (loadedCLIConfig, error) {
	if explicit && path == "" {
		return loadedCLIConfig{}, fmt.Errorf("explicit config path %q is empty", path)
	}
	if !explicit {
		path = filepath.Join(home, ".config", "listenbox", "config.yaml")
		_, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return defaultConfig, nil
			}
			return loadedCLIConfig{}, fmt.Errorf("inspect user config %q: %w", path, err)
		}
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return loadedCLIConfig{}, fmt.Errorf("load CLI config %q: %w", path, err)
	}
	return decodeCLIConfig(k, fmt.Sprintf("CLI config %q", path))
}

func decodeCLIConfig(k *koanf.Koanf, source string) (loadedCLIConfig, error) {
	raw, err := json.Marshal(k.Raw())
	if err != nil {
		return loadedCLIConfig{}, fmt.Errorf("marshal %s: %w", source, err)
	}
	var generated cliconfig.CLIConfig
	if err := json.Unmarshal(raw, &generated); err != nil {
		return loadedCLIConfig{}, fmt.Errorf("decode %s: %w", source, err)
	}
	apiOrigin, err := normalizeOrigin(generated.ApiOrigin, "API")
	if err != nil {
		return loadedCLIConfig{}, fmt.Errorf("validate api_origin in %s: %w", source, err)
	}
	dashboardOrigin, err := normalizeOrigin(generated.DashboardOrigin, "Dashboard")
	if err != nil {
		return loadedCLIConfig{}, fmt.Errorf("validate dashboard_origin in %s: %w", source, err)
	}
	return loadedCLIConfig{
		apiOrigin:       apiOrigin,
		dashboardOrigin: dashboardOrigin,
		printTraceIDs:   generated.PrintTraceIds,
	}, nil
}

func normalizeAPIOrigin(value string) (string, error) {
	return normalizeOrigin(value, "API")
}

func normalizeOrigin(value string, name string) (string, error) {
	trimmed := strings.TrimSpace(value)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse %s origin %q: %w", name, value, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if err := validateOriginComponents(parsed, scheme, value, name); err != nil {
		return "", err
	}

	host := canonicalOriginHost(parsed, scheme)
	return scheme + "://" + host, nil
}

func validateOriginComponents(parsed *url.URL, scheme string, original string, name string) error {
	switch {
	case scheme != httpScheme && scheme != httpsScheme:
		return fmt.Errorf("%s origin %q must use http or https", name, original)
	case parsed.Host == "" || parsed.Hostname() == "":
		return fmt.Errorf("%s origin %q must include a host", name, original)
	case parsed.User != nil:
		return fmt.Errorf("%s origin %q must not include user information", name, original)
	case parsed.Path != "" && parsed.Path != "/":
		return fmt.Errorf("%s origin %q must not include a path", name, original)
	case parsed.RawQuery != "" || parsed.ForceQuery:
		return fmt.Errorf("%s origin %q must not include a query", name, original)
	case parsed.Fragment != "":
		return fmt.Errorf("%s origin %q must not include a fragment", name, original)
	default:
		return nil
	}
}

func canonicalOriginHost(parsed *url.URL, scheme string) string {
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (scheme == httpScheme && port == "80") || (scheme == httpsScheme && port == "443") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return host
}

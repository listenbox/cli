package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
)

func authStatus(
	ctx context.Context,
	stdout io.Writer,
	home string,
	configPath string,
	configExplicit bool,
	defaultConfig loadedCLIConfig,
) error {
	config, err := loadCLIConfig(configPath, configExplicit, home, defaultConfig)
	if err != nil {
		return err
	}
	auth, ok := readUsableStoredAuth(defaultAuthPath(home))
	if !ok {
		return fmt.Errorf("auth status using home %q: not logged in; run listenbox login", home)
	}
	client, err := newPublicClient(config.apiOrigin, auth.APIKey, true)
	if err != nil {
		return err
	}
	response, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("auth status: %w", err)
	}
	if response == nil {
		return fmt.Errorf("auth status against %q returned no response", config.apiOrigin)
	}
	switch {
	case response.Status200 != nil:
		return printAuthStatus(stdout, *response.Status200)
	case response.Status401:
		return fmt.Errorf(
			"auth status against %q: authentication failed; run listenbox login",
			config.apiOrigin,
		)
	default:
		return fmt.Errorf("auth status returned HTTP status %d", response.StatusCode)
	}
}

func printAuthStatus(stdout io.Writer, identity publicapi.APIKeyWhoami) error {
	name := ""
	if identity.Name != nil {
		name = strings.Join(strings.Fields(*identity.Name), " ")
	}
	email := strings.TrimSpace(identity.Email)
	if email == "" {
		return fmt.Errorf("print auth status: user email %q is empty", identity.Email)
	}
	var err error
	if name == "" {
		_, err = fmt.Fprintf(stdout, "Logged in as %s\n", email)
	} else {
		_, err = fmt.Fprintf(stdout, "Logged in as %s <%s>\n", name, email)
	}
	if err != nil {
		return fmt.Errorf("print auth status for user %q: %w", email, err)
	}
	return nil
}

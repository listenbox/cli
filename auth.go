package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	authDirectoryMode os.FileMode = 0o700
	authFileMode      os.FileMode = 0o600
)

type storedAuth struct {
	APIKey string `json:"api_key"`
}

func defaultAuthPath(home string) string {
	return filepath.Join(home, ".config", "listenbox", "auth.json")
}

func readUsableStoredAuth(path string) (storedAuth, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 64<<10 {
		return storedAuth{}, false
	}
	var auth storedAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return storedAuth{}, false
	}
	if !validHeaderCredential(auth.APIKey) {
		return storedAuth{}, false
	}
	return auth, true
}

func validHeaderCredential(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func writeStoredAuth(path string, auth storedAuth) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, authDirectoryMode); err != nil {
		return fmt.Errorf("create auth directory %q: %w", directory, err)
	}
	if err := os.Chmod(directory, authDirectoryMode); err != nil {
		return fmt.Errorf("set auth directory permissions %q: %w", directory, err)
	}

	temporaryPath, err := writeTemporaryAuth(directory, auth)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace auth file %q: %w", path, err)
	}
	if err := os.Chmod(path, authFileMode); err != nil {
		return fmt.Errorf("set auth file permissions %q: %w", path, err)
	}
	return syncAuthDirectory(directory)
}

func writeTemporaryAuth(directory string, auth storedAuth) (string, error) {
	temporary, err := os.CreateTemp(directory, ".auth.json-*")
	if err != nil {
		return "", fmt.Errorf("create temporary auth file in %q: %w", directory, err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(authFileMode); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("set temporary auth permissions %q: %w", temporaryPath, err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	//nolint:gosec // Persisting the approved API key is this function's purpose.
	if err := encoder.Encode(auth); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("encode temporary auth file %q: %w", temporaryPath, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync temporary auth file %q: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close temporary auth file %q: %w", temporaryPath, err)
	}
	keep = true
	return temporaryPath, nil
}

func syncAuthDirectory(directory string) error {
	dirHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open auth directory %q for sync: %w", directory, err)
	}
	if err := dirHandle.Sync(); err != nil {
		_ = dirHandle.Close()
		return fmt.Errorf("sync auth directory %q: %w", directory, err)
	}
	if err := dirHandle.Close(); err != nil {
		return fmt.Errorf("close auth directory %q: %w", directory, err)
	}
	return nil
}

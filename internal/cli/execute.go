package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

const interruptedExitCode = 130

// Execute runs the CLI and terminates the process with its exit status.
func Execute() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if _, ok := errors.AsType[*episodeUploadInterruptedError](err); ok {
			writeCLIError(os.Stderr, err)
			return interruptedExitCode
		}
		writeCLIError(os.Stderr, err)
		return 1
	}
	return 0
}

func writeCLIError(stderr io.Writer, err error) {
	_, _ = fmt.Fprintln(stderr, err)
}

func loadEmbeddedCLIConfig() (loadedCLIConfig, error) {
	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(embeddedCLIConfig), yaml.Parser()); err != nil {
		return loadedCLIConfig{}, fmt.Errorf("load embedded CLI config: %w", err)
	}
	return decodeCLIConfig(k, "embedded CLI config")
}

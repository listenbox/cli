//nolint:mnd // Buffer sizes and argv capacities are bounded implementation constants.
package mediarunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultOutputLimit = 256 * 1024
	ffmpegExecutable   = "ffmpeg"
	ffprobeTimeout     = 30 * time.Second
	ffmpegTimeout      = 30 * time.Minute
	tracerName         = "github.com/listenbox/listenbox-cli/mediarunner"
)

var ErrOutputLimit = errors.New("media process output limit exceeded")

type Result struct {
	Stdout []byte
	Stderr []byte
}

type Runner struct {
	outputLimit int
	environment []string
}

func New() Runner {
	environment := make([]string, 0, 4)
	for _, name := range []string{"PATH", "TMPDIR", "LANG", "LC_ALL"} {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return Runner{outputLimit: defaultOutputLimit, environment: environment}
}

// Verify checks that FFmpeg and FFprobe come from one release line and expose
// every codec and muxer used by production media jobs.
func Verify(ctx context.Context) error {
	runner := New()
	ffmpegVersion, err := runner.Run(ctx, ffmpegExecutable, "-version")
	if err != nil {
		return fmt.Errorf("verify ffmpeg version: %w", err)
	}
	ffprobeVersion, err := runner.Run(ctx, "ffprobe", "-version")
	if err != nil {
		return fmt.Errorf("verify ffprobe version: %w", err)
	}
	ffmpegLine := firstLine(string(ffmpegVersion.Stdout))
	ffprobeLine := firstLine(string(ffprobeVersion.Stdout))
	if mediaReleaseLine(ffmpegLine) == "" || mediaReleaseLine(ffmpegLine) != mediaReleaseLine(ffprobeLine) {
		return fmt.Errorf("ffmpeg and ffprobe release lines differ: %q and %q", ffmpegLine, ffprobeLine)
	}
	encoders, err := runner.Run(ctx, ffmpegExecutable, "-encoders")
	if err != nil {
		return fmt.Errorf("verify ffmpeg encoders: %w", err)
	}
	for _, encoder := range []string{"libx264", "aac"} {
		if !strings.Contains(string(encoders.Stdout), encoder) {
			return fmt.Errorf("ffmpeg encoder %q is unavailable", encoder)
		}
	}
	muxers, err := runner.Run(ctx, ffmpegExecutable, "-muxers")
	if err != nil {
		return fmt.Errorf("verify ffmpeg muxers: %w", err)
	}
	for _, muxer := range []string{" hls", " mp4", " tee", " null"} {
		if !strings.Contains(string(muxers.Stdout), muxer) {
			return fmt.Errorf("ffmpeg muxer %q is unavailable", strings.TrimSpace(muxer))
		}
	}
	return nil
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return line
}

func mediaReleaseLine(versionLine string) string {
	fields := strings.Fields(versionLine)
	if len(fields) < 3 || fields[1] != "version" {
		return ""
	}
	version := strings.TrimPrefix(fields[2], "n")
	major, _, _ := strings.Cut(version, ".")
	return major
}

func (runner Runner) Run(ctx context.Context, executable string, args ...string) (Result, error) {
	return runner.run(ctx, executable, nil, nil, args...)
}

// RunWithExtraFiles runs an approved media executable with caller-owned file descriptors.
// The descriptors are inherited by the child as pipe:3, pipe:4, and so on.
func (runner Runner) RunWithExtraFiles(
	ctx context.Context, executable string, extraFiles []*os.File, args ...string,
) (Result, error) {
	for index, file := range extraFiles {
		if file == nil {
			return Result{}, fmt.Errorf("media process extra file %d is nil", index)
		}
	}
	return runner.run(ctx, executable, nil, extraFiles, args...)
}

// RunWithInput runs an approved media executable with a caller-owned streaming
// input. It is used for durable object streams so workers never need to stage a
// complete direct-media source on local disk.
func (runner Runner) RunWithInput(
	ctx context.Context, executable string, input io.Reader, args ...string,
) (Result, error) {
	if input == nil {
		return Result{}, errors.New("media process input is nil")
	}
	return runner.run(ctx, executable, input, nil, args...)
}

//nolint:nonamedreturns // Deferred span finalization records the returned process error.
func (runner Runner) run(
	ctx context.Context,
	executable string,
	input io.Reader,
	extraFiles []*os.File, args ...string,
) (result Result, retErr error) {
	timeout, err := executableTimeout(executable)
	if err != nil {
		return Result{}, err
	}
	args = requiredGlobalArgs(executable, args)
	if executable == ffmpegExecutable {
		parentSpan := trace.SpanFromContext(ctx)
		spanCtx, processSpan := parentSpan.TracerProvider().Tracer(tracerName).Start(
			ctx,
			"process.ffmpeg",
			trace.WithAttributes(attribute.String("process.command", executable)),
		)
		ctx = spanCtx
		startedAt := time.Now()
		defer func() {
			durationMilliseconds := float64(time.Since(startedAt).Microseconds()) / 1000
			duration := attribute.Float64("process.duration_ms", durationMilliseconds)
			parentSpan.SetAttributes(duration)
			processSpan.SetAttributes(duration)
			if retErr != nil {
				processSpan.RecordError(retErr)
				processSpan.SetStatus(codes.Error, retErr.Error())
			}
			processSpan.End()
		}()
	}
	processCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- executable is restricted above and argv is passed directly without a shell.
	command := exec.CommandContext(processCtx, executable, args...)
	command.Env = slices.Clone(runner.environment)
	command.Stdin = input
	command.ExtraFiles = extraFiles
	stdout := newCappedBuffer(runner.outputLimit)
	stderr := newCappedBuffer(runner.outputLimit)
	command.Stdout = stdout
	command.Stderr = stderr
	runErr := command.Run()
	result = Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if stdout.overflowed || stderr.overflowed {
		return result, fmt.Errorf("run %s: %w", executable, ErrOutputLimit)
	}
	if processCtx.Err() != nil {
		return result, fmt.Errorf("run %s: %w", executable, processCtx.Err())
	}
	if runErr != nil {
		return result, fmt.Errorf("run %s: %w", executable, runErr)
	}
	return result, nil
}

func executableTimeout(executable string) (time.Duration, error) {
	switch executable {
	case "ffprobe":
		return ffprobeTimeout, nil
	case ffmpegExecutable:
		return ffmpegTimeout, nil
	default:
		return 0, fmt.Errorf("unsupported media executable %q", executable)
	}
}

func requiredGlobalArgs(executable string, args []string) []string {
	result := make([]string, 0, len(args)+2)
	if executable == ffmpegExecutable && !slices.Contains(args, "-nostdin") {
		result = append(result, "-nostdin")
	}
	if !slices.Contains(args, "-hide_banner") {
		result = append(result, "-hide_banner")
	}
	return append(result, args...)
}

type cappedBuffer struct {
	buffer     bytes.Buffer
	limit      int
	overflowed bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.overflowed = true
		return len(value), nil
	}
	written := min(len(value), remaining)
	_, _ = buffer.buffer.Write(value[:written])
	if written < len(value) {
		buffer.overflowed = true
	}
	return len(value), nil
}

func (buffer *cappedBuffer) Bytes() []byte {
	return bytes.Clone(buffer.buffer.Bytes())
}

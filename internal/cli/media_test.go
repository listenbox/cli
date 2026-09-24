package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asticode/go-astiav"
	"gotest.tools/v3/assert"
)

func TestNativeMediaProducesAVCAACAndHLS(t *testing.T) {
	t.Parallel()
	for _, audio := range []string{"aac.m4a", "opus.webm"} {
		t.Run(audio, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			directory := t.TempDir()
			prepared := filepath.Join(directory, "audio.m4a")
			assert.NilError(t, prepareMediaAudio(ctx, filepath.Join("testdata", "media", audio), prepared))
			destination := filepath.Join(directory, "video.mp4")
			assert.NilError(t, muxMedia(ctx, filepath.Join("testdata", "media", "video.mp4"), prepared, destination))
			duration, err := packageYouTubeVideo(ctx, directory)
			assert.NilError(t, err)
			assert.Assert(t, duration >= 1 && duration <= 4, "unexpected package duration %d", duration)
			assertNativePackage(t, directory)
		})
	}
}

func assertNativePackage(t *testing.T, directory string) {
	t.Helper()
	destination := filepath.Join(directory, "video.mp4")
	audio, err := openMediaInput(destination, astiav.MediaTypeAudio)
	assert.NilError(t, err)
	defer audio.close()
	assert.Equal(t, audio.stream.CodecParameters().CodecID(), astiav.CodecIDAac)
	video, err := openMediaInput(destination, astiav.MediaTypeVideo)
	assert.NilError(t, err)
	defer video.close()
	assert.Equal(t, video.stream.CodecParameters().CodecID(), astiav.CodecIDH264)
	assert.Equal(t, video.stream.CodecParameters().Width(), 1280)
	assert.Equal(t, video.stream.CodecParameters().Height(), 720)
	data, err := os.ReadFile(destination)
	assert.NilError(t, err)
	assert.Assert(t, len(data) > 100_000 && len(data) < 2_000_000)
	moov, mdat := bytes.Index(data, []byte("moov")), bytes.Index(data, []byte("mdat"))
	assert.Assert(t, moov > 0 && mdat > moov, "MP4 must be fast-start")
	for _, kind := range []string{youtubeVideoKind, youtubeAudioKind} {
		root := filepath.Join(directory, "hls", kind)
		playlist, err := os.ReadFile(filepath.Join(root, "index.m3u8"))
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(string(playlist), "#EXT-X-ENDLIST"))
		for _, name := range []string{"init.mp4", "segment-00000.m4s"} {
			info, err := os.Stat(filepath.Join(root, name))
			assert.NilError(t, err)
			assert.Assert(t, info.Size() > 100 && info.Size() < 2_000_000)
		}
	}
}

func TestNativeMediaCancellationAndInvalidInput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output := filepath.Join(t.TempDir(), "audio.m4a")
	assert.ErrorIs(t, prepareMediaAudio(ctx, "testdata/media/opus.webm", output), context.Canceled)
	_, err := os.Stat(output)
	assert.Assert(t, os.IsNotExist(err), "cancelled preparation must not create output")
	invalid := filepath.Join(t.TempDir(), "invalid.mp4")
	assert.NilError(t, os.WriteFile(invalid, []byte("not media"), 0o600))
	assert.ErrorContains(t, prepareMediaAudio(t.Context(), invalid, output), "open media")
}

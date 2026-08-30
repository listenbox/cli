package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestInspectEpisodeSourceLeavesMediaValidationToServer(t *testing.T) {
	t.Parallel()

	sourceBytes := []byte("opaque upload bytes that only the server may probe")
	sourcePath := filepath.Join(t.TempDir(), "episode.mp4")
	assert.NilError(t, os.WriteFile(sourcePath, sourceBytes, 0o600))

	source, err := inspectEpisodeSource(sourcePath)
	assert.NilError(t, err)
	assert.Equal(t, source.ContentType, "video/mp4")
	assert.Assert(t, source.ByteLength > 0)
	assert.Assert(t, source.ByteLength < 1<<10)
	expectedHash := sha256.Sum256(sourceBytes)
	assert.Equal(t, source.SHA256, hex.EncodeToString(expectedHash[:]))
}

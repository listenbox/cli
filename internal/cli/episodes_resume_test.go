//nolint:lll // Resume fixtures keep the complete strong file identity visible.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	publicapi "github.com/listenbox/listenbox-cli/publicapi"
	"gotest.tools/v3/assert"
)

func TestEpisodeResumeLookupIsScopedByShowAndFileSHA(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "source.mp3")
	args := episodesCreateArguments{
		Show: testShowsCreateSlug, Title: testEpisodeTitle, File: path,
		Publication: publicapi.EpisodePublicationDraft,
	}
	source := episodeSourceIdentity{AbsolutePath: path, ByteLength: 10, ContentType: episodeAudioMIME, SHA256: strings.Repeat("a", 64)}
	resumePath, first, err := loadOrCreateEpisodeResume(home, args, source)
	assert.NilError(t, err)
	secondPath := filepath.Join(t.TempDir(), "renamed.mp3")
	resumed := source
	resumed.AbsolutePath = secondPath
	changedArgs := args
	changedArgs.Title = "Changed local command metadata"
	changedArgs.Publication = publicapi.EpisodePublicationPublish
	resolvedPath, second, err := loadOrCreateEpisodeResume(home, changedArgs, resumed)
	assert.NilError(t, err)
	assert.Equal(t, resolvedPath, resumePath)
	assert.Equal(t, second.FileSHA256, first.FileSHA256)
	assert.Equal(t, second.FilePath, secondPath)
	_, statErr := os.Stat(resumePath)
	assert.NilError(t, statErr)
	changedHash := source
	changedHash.SHA256 = strings.Repeat("b", 64)
	newPath, _, err := loadOrCreateEpisodeResume(home, args, changedHash)
	assert.NilError(t, err)
	assert.Assert(t, newPath != resumePath)
}

func TestEpisodeResumeRejectsExpiredAttemptAndPreservesDraftRecord(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "source.mp3")
	args := episodesCreateArguments{
		Show: testShowsCreateSlug, Title: testEpisodeTitle, File: path,
		Publication: publicapi.EpisodePublicationDraft,
	}
	source := episodeSourceIdentity{AbsolutePath: path, ByteLength: 10, ContentType: episodeAudioMIME, SHA256: strings.Repeat("a", 64)}
	resumePath, record, err := loadOrCreateEpisodeResume(home, args, source)
	assert.NilError(t, err)
	record.ExpiresAt = time.Now().Add(-time.Minute).UnixMilli()
	record.Phase = "uploading"
	assert.NilError(t, writeEpisodeResume(resumePath, record))
	_, preserved, err := loadOrCreateEpisodeResume(home, args, source)
	assert.ErrorContains(t, err, "expired")
	assert.Equal(t, preserved.UploadSessionID, record.UploadSessionID)
}

func TestSavedEpisodeProgressCountsAcceptedPartsOnly(t *testing.T) {
	t.Parallel()
	parts := map[int32]episodeResumePart{
		1: {PartNumber: 1, Size: 5},
	}
	var output bytes.Buffer
	assert.NilError(t, printSavedEpisodeProgress(&output, parts, 10))
	assert.Equal(t, output.String(), "Source upload: 50% saved\n")
	parts[2] = episodeResumePart{PartNumber: 2, Size: 5}
	assert.NilError(t, printSavedEpisodeProgress(&output, parts, 10))
	assert.Equal(t, output.String(), "Source upload: 50% saved\nSource upload: 100% saved\n")
}

func TestEpisodeResumeRecordContainsUploadFactsOnly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "source.mp3")
	args := episodesCreateArguments{
		Show: testShowsCreateSlug, Title: testEpisodeTitle, File: path,
		Publication: publicapi.EpisodePublicationDraft,
	}
	source := episodeSourceIdentity{AbsolutePath: path, ByteLength: 10, ContentType: episodeAudioMIME, SHA256: strings.Repeat("a", 64)}
	resumePath, _, err := loadOrCreateEpisodeResume(home, args, source)
	assert.NilError(t, err)
	raw, err := os.ReadFile(resumePath)
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Contains(raw, []byte("upload_url")))
	assert.Assert(t, !bytes.Contains(raw, []byte("api_key")))
	assert.Assert(t, !bytes.Contains(raw, []byte("publication")))
	info, err := os.Stat(resumePath)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600))
}

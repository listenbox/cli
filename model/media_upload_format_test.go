package model

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestMediaUploadFormatCanonicalMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fileName    string
		contentType string
		format      string
		podcastType PodcastType
	}{
		{fileName: "source.mp3", contentType: "audio/mpeg", format: "mp3", podcastType: AudioPodcastType()},
		{fileName: "source.m4a", contentType: "audio/mp4", format: "m4a", podcastType: AudioPodcastType()},
		{fileName: "source.wav", contentType: "audio/wav", format: "wav", podcastType: AudioPodcastType()},
		{fileName: "source.flac", contentType: "audio/flac", format: "flac", podcastType: AudioPodcastType()},
		{fileName: "source.mp4", contentType: mediaUploadVideoMP4ContentType, format: "mp4", podcastType: VideoPodcastType()},
		{fileName: "source.m4v", contentType: mediaUploadVideoMP4ContentType, format: "m4v", podcastType: VideoPodcastType()},
		{fileName: "source.mov", contentType: "video/quicktime", format: "mov", podcastType: VideoPodcastType()},
	}
	for _, testCase := range tests {
		t.Run(testCase.format, func(t *testing.T) {
			t.Parallel()
			format, err := NewMediaUploadFormat(testCase.fileName, testCase.contentType)
			assert.NilError(t, err)
			assert.Equal(t, format.String(), testCase.format)
			assert.Equal(t, format.ContentType(), testCase.contentType)
			assert.Equal(t, format.PodcastType(), testCase.podcastType)
		})
	}
}

func TestMediaUploadFormatRejectsUnsupportedOrMismatchedInput(t *testing.T) {
	t.Parallel()

	_, err := NewMediaUploadFormat("source.ogg", "audio/ogg")
	assert.ErrorContains(t, err, "accepted extensions")
	_, err = NewMediaUploadFormat("source.M4V", "video/quicktime")
	assert.ErrorContains(t, err, "requires content type \""+mediaUploadVideoMP4ContentType+"\"")
}

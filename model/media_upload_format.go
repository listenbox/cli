package model

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MediaUploadFormat is one supported source file format at the upload boundary.
type MediaUploadFormat struct {
	value string
}

const (
	mediaUploadFormatMP3Value      = "mp3"
	mediaUploadFormatM4AValue      = "m4a"
	mediaUploadFormatWAVValue      = "wav"
	mediaUploadFormatFLACValue     = "flac"
	mediaUploadFormatMP4Value      = "mp4"
	mediaUploadFormatM4VValue      = "m4v"
	mediaUploadFormatMOVValue      = "mov"
	mediaUploadVideoMP4ContentType = "video/mp4"
)

var allowedMediaUploadFormatValues = [...]string{
	mediaUploadFormatMP3Value,
	mediaUploadFormatM4AValue,
	mediaUploadFormatWAVValue,
	mediaUploadFormatFLACValue,
	mediaUploadFormatMP4Value,
	mediaUploadFormatM4VValue,
	mediaUploadFormatMOVValue,
}

func MP3MediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatMP3Value}
}

func M4AMediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatM4AValue}
}

func WAVMediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatWAVValue}
}

func FLACMediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatFLACValue}
}

func MP4MediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatMP4Value}
}

func M4VMediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatM4VValue}
}

func MOVMediaUploadFormat() MediaUploadFormat {
	return MediaUploadFormat{value: mediaUploadFormatMOVValue}
}

func NewMediaUploadFormatFromValue(value string) (MediaUploadFormat, error) {
	switch value {
	case mediaUploadFormatMP3Value:
		return MP3MediaUploadFormat(), nil
	case mediaUploadFormatM4AValue:
		return M4AMediaUploadFormat(), nil
	case mediaUploadFormatWAVValue:
		return WAVMediaUploadFormat(), nil
	case mediaUploadFormatFLACValue:
		return FLACMediaUploadFormat(), nil
	case mediaUploadFormatMP4Value:
		return MP4MediaUploadFormat(), nil
	case mediaUploadFormatM4VValue:
		return M4VMediaUploadFormat(), nil
	case mediaUploadFormatMOVValue:
		return MOVMediaUploadFormat(), nil
	default:
		return MediaUploadFormat{}, fmt.Errorf(
			"unsupported media upload format %q; accepted values: %s",
			value,
			strings.Join(allowedMediaUploadFormatValues[:], ", "),
		)
	}
}

func NewMediaUploadFormatFromFileName(fileName string) (MediaUploadFormat, error) {
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(fileName))), ".")
	if extension == "" {
		return MediaUploadFormat{}, errorsUnsupportedMediaUploadFile(fileName)
	}
	format, err := NewMediaUploadFormatFromValue(extension)
	if err != nil {
		return MediaUploadFormat{}, errorsUnsupportedMediaUploadFile(fileName)
	}
	return format, nil
}

func NewMediaUploadFormat(fileName string, contentType string) (MediaUploadFormat, error) {
	format, err := NewMediaUploadFormatFromFileName(fileName)
	if err != nil {
		return MediaUploadFormat{}, err
	}
	if contentType != format.ContentType() {
		return MediaUploadFormat{}, fmt.Errorf(
			"media upload file %q requires content type %q, got %q",
			fileName,
			format.ContentType(),
			contentType,
		)
	}
	return format, nil
}

func errorsUnsupportedMediaUploadFile(fileName string) error {
	return fmt.Errorf(
		"unsupported media upload file %q; accepted extensions: .%s",
		fileName,
		strings.Join(allowedMediaUploadFormatValues[:], ", ."),
	)
}

func (format MediaUploadFormat) String() string {
	return format.value
}

func (format MediaUploadFormat) ContentType() string {
	switch format.value {
	case mediaUploadFormatMP3Value:
		return "audio/mpeg"
	case mediaUploadFormatM4AValue:
		return "audio/mp4"
	case mediaUploadFormatWAVValue:
		return "audio/wav"
	case mediaUploadFormatFLACValue:
		return "audio/flac"
	case mediaUploadFormatMP4Value, mediaUploadFormatM4VValue:
		return mediaUploadVideoMP4ContentType
	case mediaUploadFormatMOVValue:
		return "video/quicktime"
	default:
		return ""
	}
}

func (format MediaUploadFormat) PodcastType() PodcastType {
	switch format.value {
	case mediaUploadFormatMP3Value,
		mediaUploadFormatM4AValue,
		mediaUploadFormatWAVValue,
		mediaUploadFormatFLACValue:
		return AudioPodcastType()
	case mediaUploadFormatMP4Value, mediaUploadFormatM4VValue, mediaUploadFormatMOVValue:
		return VideoPodcastType()
	default:
		return PodcastType{}
	}
}

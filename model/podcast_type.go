// Package model defines contracts shared by the Listenbox CLI and API services.
package model

import "fmt"

// PodcastType is an opaque podcast type enum wrapper.
//
// It intentionally wraps the podcast type string in a struct so Go rejects raw
// string literals at compile time for internal function parameters.
type PodcastType struct {
	value string
}

const (
	audioPodcastTypeValue = "audio"
	videoPodcastTypeValue = "video"
)

var allowedPodcastTypeValues = [...]string{
	audioPodcastTypeValue,
	videoPodcastTypeValue,
}

func AudioPodcastType() PodcastType {
	return PodcastType{value: audioPodcastTypeValue}
}

func VideoPodcastType() PodcastType {
	return PodcastType{value: videoPodcastTypeValue}
}

func NewPodcastTypeFromValue(value string) (PodcastType, error) {
	switch value {
	case audioPodcastTypeValue:
		return AudioPodcastType(), nil

	case videoPodcastTypeValue:
		return VideoPodcastType(), nil
	}

	return PodcastType{}, fmt.Errorf(
		"invalid podcast type %q; accepted values: %s, %s",
		value,
		allowedPodcastTypeValues[0],
		allowedPodcastTypeValues[1],
	)
}

func (podcastType PodcastType) String() string {
	return podcastType.value
}

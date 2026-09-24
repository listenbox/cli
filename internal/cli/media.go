package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/asticode/go-astiav"
)

const (
	mediaFastStart = "+faststart"
	mediaMovFlags  = "movflags"
)

// A mediaInput owns its demuxer and a single lookahead packet. Only downloaded
// local files enter FFmpeg; HTTP and cancellation remain owned by the importer.
type mediaInput struct {
	format *astiav.FormatContext
	stream *astiav.Stream
	packet *astiav.Packet
	ready  bool
}

func openMediaInput(path string, kind astiav.MediaType) (*mediaInput, error) {
	input := &mediaInput{format: astiav.AllocFormatContext(), packet: astiav.AllocPacket()}
	if input.format == nil || input.packet == nil {
		input.close()
		return nil, errors.New("allocate native media input")
	}
	if err := input.format.OpenInput(path, nil, nil); err != nil {
		input.close()
		return nil, fmt.Errorf("open media %q: %w", path, err)
	}
	if err := input.format.FindStreamInfo(nil); err != nil {
		input.close()
		return nil, fmt.Errorf("inspect media %q: %w", path, err)
	}
	for _, stream := range input.format.Streams() {
		if stream.CodecParameters().MediaType() == kind {
			input.stream = stream
			return input, nil
		}
	}
	input.close()
	return nil, fmt.Errorf("media %q has no %s stream", path, kind)
}

func (input *mediaInput) close() {
	if input.packet != nil {
		input.packet.Free()
	}
	if input.format != nil {
		input.format.CloseInput()
		input.format.Free()
	}
}

func (input *mediaInput) next(ctx context.Context) error {
	input.ready = false
	input.packet.Unref()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := input.format.ReadFrame(input.packet); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				return nil
			}
			return fmt.Errorf("read media packet: %w", err)
		}
		if input.packet.StreamIndex() == input.stream.Index() {
			input.ready = true
			return nil
		}
		input.packet.Unref()
	}
}

func (input *mediaInput) timestamp() int64 {
	timestamp := input.packet.Dts()
	if timestamp == astiav.NoPtsValue {
		timestamp = input.packet.Pts()
	}
	if timestamp == astiav.NoPtsValue {
		timestamp = 0
	}
	return astiav.RescaleQ(timestamp, input.stream.TimeBase(), astiav.NewRational(1, int(time.Second/time.Microsecond)))
}

type mediaOutput struct {
	format *astiav.FormatContext
	io     *astiav.IOContext
}

func openMediaOutput(path string) (*mediaOutput, error) {
	format, err := astiav.AllocOutputFormatContext(nil, "", path)
	if err != nil {
		return nil, fmt.Errorf("allocate output %q: %w", path, err)
	}
	output := &mediaOutput{format: format}
	if !format.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		output.io, err = astiav.OpenIOContext(path, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if err != nil {
			format.Free()
			return nil, fmt.Errorf("open output %q: %w", path, err)
		}
		format.SetPb(output.io)
	}
	return output, nil
}

func (output *mediaOutput) close() error {
	var err error
	if output.io != nil {
		err = output.io.Close()
	}
	output.format.Free()
	return err
}

func (output *mediaOutput) copyStream(input *mediaInput) (*astiav.Stream, error) {
	stream := output.format.NewStream(nil)
	if stream == nil {
		return nil, errors.New("allocate output stream")
	}
	if err := input.stream.CodecParameters().Copy(stream.CodecParameters()); err != nil {
		return nil, err
	}
	stream.CodecParameters().SetCodecTag(0)
	stream.SetTimeBase(input.stream.TimeBase())
	return stream, nil
}

func (output *mediaOutput) header(options map[string]string) error {
	dictionary := astiav.NewDictionary()
	defer dictionary.Free()
	for key, value := range options {
		if err := dictionary.Set(key, value, 0); err != nil {
			return err
		}
	}
	if err := output.format.WriteHeader(dictionary); err != nil {
		return fmt.Errorf("write media header: %w", err)
	}
	return nil
}

func (output *mediaOutput) write(input *mediaInput, stream *astiav.Stream) error {
	input.packet.RescaleTs(input.stream.TimeBase(), stream.TimeBase())
	input.packet.SetStreamIndex(stream.Index())
	input.packet.SetPos(-1)
	if err := output.format.WriteInterleavedFrame(input.packet); err != nil {
		return fmt.Errorf("write media packet: %w", err)
	}
	return nil
}

func muxMedia(ctx context.Context, videoPath, audioPath, destination string) (result error) {
	video, err := openMediaInput(videoPath, astiav.MediaTypeVideo)
	if err != nil {
		return err
	}
	defer video.close()
	audio, err := openMediaInput(audioPath, astiav.MediaTypeAudio)
	if err != nil {
		return err
	}
	defer audio.close()
	output, err := openMediaOutput(destination)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, output.close()) }()
	videoStream, err := output.copyStream(video)
	if err != nil {
		return err
	}
	audioStream, err := output.copyStream(audio)
	if err != nil {
		return err
	}
	if err := output.header(map[string]string{mediaMovFlags: mediaFastStart}); err != nil {
		return err
	}
	if err := video.next(ctx); err != nil {
		return err
	}
	if err := audio.next(ctx); err != nil {
		return err
	}
	if !video.ready || !audio.ready {
		return errors.New("downloaded media stream is empty")
	}
	return interleaveMedia(ctx, output, video, audio, videoStream, audioStream)
}

func interleaveMedia(
	ctx context.Context, output *mediaOutput, video, audio *mediaInput,
	videoStream, audioStream *astiav.Stream,
) error {
	// Reading both inputs in timestamp order keeps muxer buffering bounded even
	// for long recordings. Never enqueue the entire video before the audio.
	for video.ready || audio.ready {
		input, stream := audio, audioStream
		if video.ready && (!audio.ready || video.timestamp() <= audio.timestamp()) {
			input, stream = video, videoStream
		}
		if err := output.write(input, stream); err != nil {
			return err
		}
		if err := input.next(ctx); err != nil {
			return err
		}
	}
	return output.format.WriteTrailer()
}

func remuxMedia(
	ctx context.Context, source, destination string, kind astiav.MediaType, options map[string]string,
) (result error) {
	input, err := openMediaInput(source, kind)
	if err != nil {
		return err
	}
	defer input.close()
	output, err := openMediaOutput(destination)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, output.close()) }()
	stream, err := output.copyStream(input)
	if err != nil {
		return err
	}
	if err := output.header(options); err != nil {
		return err
	}
	count := 0
	for {
		if err := input.next(ctx); err != nil {
			return err
		}
		if !input.ready {
			break
		}
		if err := output.write(input, stream); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return errors.New("downloaded media stream is empty")
	}
	return output.format.WriteTrailer()
}

func mediaDimensions(source string) (int, int, error) {
	input, err := openMediaInput(source, astiav.MediaTypeVideo)
	if err != nil {
		return 0, 0, err
	}
	defer input.close()
	parameters := input.stream.CodecParameters()
	width, height := parameters.Width(), parameters.Height()
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("invalid video dimensions %dx%d", width, height)
	}
	return width, height, nil
}

func packageMediaTrack(ctx context.Context, source, root string, kind astiav.MediaType) error {
	return remuxMedia(ctx, source, filepath.Join(root, "index.m3u8"), kind, map[string]string{
		"hls_time": "6", "hls_playlist_type": "vod", "hls_segment_type": "fmp4",
		"hls_fmp4_init_filename": "init.mp4", "hls_segment_filename": filepath.Join(root, "segment-%05d.m4s"),
	})
}

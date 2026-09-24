package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/asticode/go-astiav"
)

const (
	mediaAudioRate     = 48000
	mediaStereoBitrate = 128000
)

func prepareMediaAudio(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := openMediaInput(source, astiav.MediaTypeAudio)
	if err != nil {
		return err
	}
	defer input.close()
	if input.stream.CodecParameters().CodecID() == astiav.CodecIDAac {
		return remuxMedia(ctx, source, destination, astiav.MediaTypeAudio, map[string]string{mediaMovFlags: mediaFastStart})
	}
	return transcodeMediaAudio(ctx, input, destination)
}

type mediaAudioTranscoder struct {
	decoder, encoder *astiav.CodecContext
	resampler        *astiav.SoftwareResampleContext
	fifo             *astiav.AudioFifo
	output           *mediaOutput
	stream           *astiav.Stream
	timestamp        int64
}

func (transcoder *mediaAudioTranscoder) close() {
	if transcoder.decoder != nil {
		transcoder.decoder.Free()
	}
	if transcoder.encoder != nil {
		transcoder.encoder.Free()
	}
	if transcoder.resampler != nil {
		transcoder.resampler.Free()
	}
	if transcoder.fifo != nil {
		transcoder.fifo.Free()
	}
}

func (transcoder *mediaAudioTranscoder) openCodecs(input *mediaInput) error {
	decoder := astiav.FindDecoder(input.stream.CodecParameters().CodecID())
	encoder := astiav.FindEncoder(astiav.CodecIDAac)
	if decoder == nil || encoder == nil {
		return errors.New("bundled audio codec is unavailable")
	}
	transcoder.decoder = astiav.AllocCodecContext(decoder)
	transcoder.encoder = astiav.AllocCodecContext(encoder)
	if transcoder.decoder == nil || transcoder.encoder == nil {
		return errors.New("allocate audio codec")
	}
	if err := input.stream.CodecParameters().ToCodecContext(transcoder.decoder); err != nil {
		return err
	}
	if err := transcoder.decoder.Open(decoder, nil); err != nil {
		return err
	}
	if transcoder.decoder.SampleRate() <= 0 || transcoder.decoder.ChannelLayout().Channels() <= 0 {
		return errors.New("invalid input audio rate or channel layout")
	}
	layout := astiav.ChannelLayoutStereo
	bitrate := int64(mediaStereoBitrate)
	if transcoder.decoder.ChannelLayout().Channels() == 1 {
		layout = astiav.ChannelLayoutMono
		bitrate = 64000
	}
	transcoder.encoder.SetChannelLayout(layout)
	transcoder.encoder.SetSampleRate(mediaAudioRate)
	transcoder.encoder.SetSampleFormat(astiav.SampleFormatFltp)
	transcoder.encoder.SetBitRate(bitrate)
	transcoder.encoder.SetTimeBase(astiav.NewRational(1, mediaAudioRate))
	transcoder.encoder.SetFlags(astiav.NewCodecContextFlags(astiav.CodecContextFlagGlobalHeader))
	return transcoder.encoder.Open(encoder, nil)
}

func (transcoder *mediaAudioTranscoder) openOutput() error {
	transcoder.stream = transcoder.output.format.NewStream(nil)
	if transcoder.stream == nil {
		return errors.New("allocate AAC output stream")
	}
	transcoder.stream.SetTimeBase(transcoder.encoder.TimeBase())
	if err := transcoder.encoder.ToCodecParameters(transcoder.stream.CodecParameters()); err != nil {
		return err
	}
	if err := transcoder.output.header(map[string]string{mediaMovFlags: mediaFastStart}); err != nil {
		return err
	}
	transcoder.resampler = astiav.AllocSoftwareResampleContext()
	transcoder.fifo = astiav.AllocAudioFifo(
		transcoder.encoder.SampleFormat(), transcoder.encoder.ChannelLayout().Channels(), 1,
	)
	if transcoder.resampler == nil || transcoder.fifo == nil {
		return errors.New("allocate audio resampler")
	}
	return nil
}

func transcodeMediaAudio(ctx context.Context, input *mediaInput, destination string) (result error) {
	transcoder := &mediaAudioTranscoder{}
	defer transcoder.close()
	if err := transcoder.openCodecs(input); err != nil {
		return fmt.Errorf("open audio codecs: %w", err)
	}
	output, err := openMediaOutput(destination)
	if err != nil {
		return err
	}
	transcoder.output = output
	defer func() { result = errors.Join(result, output.close()) }()
	if err := transcoder.openOutput(); err != nil {
		return err
	}
	return transcoder.run(ctx, input)
}

func (transcoder *mediaAudioTranscoder) run(ctx context.Context, input *mediaInput) error {
	for {
		if err := input.next(ctx); err != nil {
			return err
		}
		if !input.ready {
			break
		}
		if err := transcoder.decode(ctx, input.packet); err != nil {
			return err
		}
	}
	if err := transcoder.decode(ctx, nil); err != nil {
		return err
	}
	return transcoder.finish(ctx)
}

func (transcoder *mediaAudioTranscoder) finish(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		samples, err := transcoder.resample(nil)
		if err != nil {
			return err
		}
		if samples == 0 {
			break
		}
		if err := transcoder.encodeReady(ctx, false); err != nil {
			return err
		}
	}
	if err := transcoder.encodeReady(ctx, true); err != nil {
		return err
	}
	if transcoder.timestamp == 0 {
		return errors.New("downloaded audio stream is empty")
	}
	if err := transcoder.encoder.SendFrame(nil); err != nil {
		return err
	}
	if err := transcoder.drainEncoder(); err != nil {
		return err
	}
	return transcoder.output.format.WriteTrailer()
}

func mediaCodecWaiting(err error) bool {
	return errors.Is(err, astiav.ErrEagain) || errors.Is(err, astiav.ErrEof)
}

func (transcoder *mediaAudioTranscoder) decode(ctx context.Context, packet *astiav.Packet) error {
	if err := transcoder.decoder.SendPacket(packet); err != nil {
		return err
	}
	frame := astiav.AllocFrame()
	if frame == nil {
		return errors.New("allocate decoded audio frame")
	}
	defer frame.Free()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := transcoder.decoder.ReceiveFrame(frame); err != nil {
			if mediaCodecWaiting(err) {
				return nil
			}
			return fmt.Errorf("decode audio frame: %w", err)
		}
		if _, err := transcoder.resample(frame); err != nil {
			return err
		}
		frame.Unref()
		if err := transcoder.encodeReady(ctx, false); err != nil {
			return err
		}
	}
}

func (transcoder *mediaAudioTranscoder) resample(input *astiav.Frame) (int, error) {
	frame := astiav.AllocFrame()
	if frame == nil {
		return 0, errors.New("allocate resampled audio frame")
	}
	defer frame.Free()
	frame.SetChannelLayout(transcoder.encoder.ChannelLayout())
	frame.SetSampleFormat(transcoder.encoder.SampleFormat())
	frame.SetSampleRate(mediaAudioRate)
	// Let swresample allocate enough space for the input and retained delay.
	if err := transcoder.resampler.ConvertFrame(input, frame); err != nil {
		return 0, fmt.Errorf("resample audio: %w", err)
	}
	if frame.NbSamples() > 0 {
		if _, err := transcoder.fifo.Write(frame); err != nil {
			return 0, err
		}
	}
	return frame.NbSamples(), nil
}

func (transcoder *mediaAudioTranscoder) encodeReady(ctx context.Context, finish bool) error {
	size := transcoder.encoder.FrameSize()
	for transcoder.fifo.Size() >= size || (finish && transcoder.fifo.Size() > 0) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := transcoder.encodeFrame(min(size, transcoder.fifo.Size())); err != nil {
			return err
		}
	}
	return nil
}

func (transcoder *mediaAudioTranscoder) encodeFrame(samples int) error {
	frame := astiav.AllocFrame()
	if frame == nil {
		return errors.New("allocate encoder audio frame")
	}
	defer frame.Free()
	frame.SetChannelLayout(transcoder.encoder.ChannelLayout())
	frame.SetSampleFormat(transcoder.encoder.SampleFormat())
	frame.SetSampleRate(mediaAudioRate)
	frame.SetNbSamples(samples)
	if err := frame.AllocBuffer(0); err != nil {
		return err
	}
	if _, err := transcoder.fifo.Read(frame); err != nil {
		return err
	}
	frame.SetPts(transcoder.timestamp)
	transcoder.timestamp += int64(samples)
	if err := transcoder.encoder.SendFrame(frame); err != nil {
		return err
	}
	return transcoder.drainEncoder()
}

func (transcoder *mediaAudioTranscoder) drainEncoder() error {
	packet := astiav.AllocPacket()
	if packet == nil {
		return errors.New("allocate encoded audio packet")
	}
	defer packet.Free()
	for {
		if err := transcoder.encoder.ReceivePacket(packet); err != nil {
			if mediaCodecWaiting(err) {
				return nil
			}
			return fmt.Errorf("encode AAC packet: %w", err)
		}
		packet.RescaleTs(transcoder.encoder.TimeBase(), transcoder.stream.TimeBase())
		packet.SetStreamIndex(transcoder.stream.Index())
		packet.SetPos(-1)
		if err := transcoder.output.format.WriteInterleavedFrame(packet); err != nil {
			return err
		}
		packet.Unref()
	}
}

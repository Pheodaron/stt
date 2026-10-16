package packeter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
)

const (
	maxAudioLate      = 10 // Sequence numbers, about 200 ms with 20 ms packets.
	readPollInterval  = 50 * time.Millisecond
	idleDrainInterval = 100 * time.Millisecond
)

type PacketCb func(buf []byte) error

// Options bounds chunk latency and backpressure. Zero values select defaults.
type Options struct {
	FlushInterval  time.Duration
	MaxBufferBytes int
	EnqueueTimeout time.Duration
}

func DefaultOptions() Options {
	return Options{FlushInterval: time.Second, MaxBufferBytes: 10000, EnqueueTimeout: 2 * time.Second}
}

type rtpReader interface {
	ReadRTP() (*rtp.Packet, interceptor.Attributes, error)
	SetReadDeadline(time.Time) error
}

type BufferedPacketer struct {
	sb       *samplebuilder.SampleBuilder
	track    rtpReader
	ch       chan<- *bytes.Buffer
	options  Options
	channels uint16
}

func NewOpusBuffer(track *webrtc.TrackRemote, ch chan *bytes.Buffer) (*BufferedPacketer, error) {
	return NewOpusBufferWithOptions(track, ch, Options{})
}

func NewOpusBufferWithOptions(track *webrtc.TrackRemote, ch chan *bytes.Buffer, options Options) (*BufferedPacketer, error) {
	if track == nil {
		return nil, errors.New("nil audio track")
	}
	codec := track.Codec()
	if !strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus) || codec.ClockRate != 48000 {
		return nil, fmt.Errorf("expected Opus/48000, got %s/%d", codec.MimeType, codec.ClockRate)
	}
	return newOpusBuffer(track, ch, codec.Channels, options)
}

func newOpusBuffer(track rtpReader, ch chan<- *bytes.Buffer, channels uint16, options Options) (*BufferedPacketer, error) {
	defaults := DefaultOptions()
	if options.FlushInterval == 0 {
		options.FlushInterval = defaults.FlushInterval
	}
	if options.MaxBufferBytes == 0 {
		options.MaxBufferBytes = defaults.MaxBufferBytes
	}
	if options.EnqueueTimeout == 0 {
		options.EnqueueTimeout = defaults.EnqueueTimeout
	}
	if options.FlushInterval < 0 || options.MaxBufferBytes < 0 || options.EnqueueTimeout < 0 {
		return nil, errors.New("packeter limits must be positive")
	}
	if channels == 0 {
		channels = 2
	} // Opus RTP's default channel mapping.
	if channels > 2 {
		return nil, fmt.Errorf("unsupported channel count: %d", channels)
	}
	if ch == nil {
		return nil, errors.New("nil audio chunk channel")
	}
	return &BufferedPacketer{
		sb:    samplebuilder.New(maxAudioLate, &codecs.OpusPacket{}, 48000),
		track: track, ch: ch, channels: channels, options: options,
	}, nil
}

// Start retains the original API. The caller owns and closes the output channel.
func (t *BufferedPacketer) Start() error { return t.StartContext(context.Background()) }

// StartContext owns the builder and Ogg data on one goroutine. Read deadlines
// allow the clock to advance even when DTX/mute stops RTP completely.
func (t *BufferedPacketer) StartContext(ctx context.Context) error {
	defer t.track.SetReadDeadline(time.Time{})
	var chunk *opusOgg
	var firstAudioAt, lastPacketAt time.Time

	flush := func() error {
		if chunk == nil || chunk.packets == 0 {
			return nil
		}
		buf := chunk.finish()
		timer := time.NewTimer(t.options.EnqueueTimeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("audio chunk queue blocked: enqueue timeout")
		case t.ch <- buf:
			// Ownership is transferred: no writes to this backing array follow.
			chunk = nil
			firstAudioAt = time.Time{}
			return nil
		}
	}
	drain := func(force bool) error {
		for {
			var packets []*rtp.Packet
			if force {
				packets = t.sb.ForcePopPackets()
			} else {
				packets = t.sb.PopPackets()
			}
			if len(packets) == 0 {
				return nil
			}
			for _, packet := range packets {
				if len(packet.Payload) == 0 {
					continue
				}
				if chunk == nil {
					chunk = newOpusOgg(t.channels)
					if firstAudioAt.IsZero() {
						firstAudioAt = time.Now()
					}
				}
				if err := chunk.writePacket(packet.Payload); err != nil {
					return fmt.Errorf("pack Opus: %w", err)
				}
				if len(chunk.data) >= t.options.MaxBufferBytes {
					if err := flush(); err != nil {
						return err
					}
				}
			}
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := time.Now()
		if !lastPacketAt.IsZero() && now.Sub(lastPacketAt) >= idleDrainInterval {
			if err := drain(true); err != nil {
				return err
			}
		}
		if !firstAudioAt.IsZero() && now.Sub(firstAudioAt) >= t.options.FlushInterval {
			if err := drain(true); err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
			// Headers or a lost/empty packet alone must not keep an expired timer.
			firstAudioAt = time.Time{}
		}

		deadline := time.Now().Add(readPollInterval)
		if !firstAudioAt.IsZero() {
			if due := firstAudioAt.Add(t.options.FlushInterval); due.Before(deadline) {
				deadline = due
			}
		}
		if err := t.track.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("set RTP read deadline: %w", err)
		}
		packet, _, err := t.track.ReadRTP()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			// Drain and deliver valid trailing audio on EOF and read failures.
			if drainErr := drain(true); drainErr != nil {
				return drainErr
			}
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return fmt.Errorf("track.ReadRTP: %w", err)
		}
		if packet == nil || len(packet.Payload) == 0 {
			continue
		}
		lastPacketAt = time.Now()
		if firstAudioAt.IsZero() {
			firstAudioAt = lastPacketAt
		}
		t.sb.Push(packet)
		if err := drain(false); err != nil {
			return err
		}
	}
}

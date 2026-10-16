package packeter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

type readEvent struct {
	packet *rtp.Packet
	err    error
}
type fakeTrack struct {
	events   chan readEvent
	mu       sync.Mutex
	deadline time.Time
}

func (f *fakeTrack) SetReadDeadline(d time.Time) error {
	f.mu.Lock()
	f.deadline = d
	f.mu.Unlock()
	return nil
}
func (f *fakeTrack) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	f.mu.Lock()
	deadline := f.deadline
	f.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case event, ok := <-f.events:
		if !ok {
			return nil, nil, io.EOF
		}
		return event.packet, nil, event.err
	case <-timer.C:
		return nil, nil, os.ErrDeadlineExceeded
	}
}
func packet(seq uint16) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 960}, Payload: []byte{0xf8, byte(seq), 0xfe}}
}
func startPacketer(t *testing.T, track *fakeTrack, output chan *bytes.Buffer, options Options) (context.CancelFunc, <-chan error) {
	t.Helper()
	p, err := newOpusBuffer(track, output, 1, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.StartContext(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("packeter did not stop")
		}
	})
	return cancel, done
}
func getChunk(t *testing.T, output <-chan *bytes.Buffer) *bytes.Buffer {
	t.Helper()
	select {
	case chunk := <-output:
		return chunk
	case <-time.After(time.Second):
		t.Fatal("audio stuck while no new RTP arrives")
		return nil
	}
}
func TestShortPhraseFlushesWithoutNextRTP(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 4)}
	output := make(chan *bytes.Buffer, 4)
	startPacketer(t, track, output, Options{FlushInterval: 30 * time.Millisecond})
	track.events <- readEvent{packet: packet(1)}
	chunk := getChunk(t, output)
	if !bytes.Contains(chunk.Bytes(), packet(1).Payload) {
		t.Fatal("short phrase missing")
	}
	select {
	case <-output:
		t.Fatal("empty/duplicate chunk during silence")
	case <-time.After(70 * time.Millisecond):
	}
}
func TestContinuousRTPDoesNotResetFlushDeadline(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 4)}
	output := make(chan *bytes.Buffer, 10)
	cancel, _ := startPacketer(t, track, output, Options{FlushInterval: 30 * time.Millisecond})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		var seq uint16
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				seq++
				select {
				case track.events <- readEvent{packet: packet(seq)}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	getChunk(t, output)
	getChunk(t, output)
	cancel()
}
func TestEOFAndReadErrorFlushTrailingAudio(t *testing.T) {
	for _, end := range []error{io.EOF, errors.New("transport failed")} {
		t.Run(end.Error(), func(t *testing.T) {
			track := &fakeTrack{events: make(chan readEvent, 4)}
			output := make(chan *bytes.Buffer, 4)
			_, done := startPacketer(t, track, output, Options{FlushInterval: time.Hour})
			track.events <- readEvent{packet: packet(1)}
			track.events <- readEvent{err: end}
			if !bytes.Contains(getChunk(t, output).Bytes(), packet(1).Payload) {
				t.Fatal("tail missing")
			}
			if err := <-done; !errors.Is(err, end) {
				t.Fatalf("got %v, want %v", err, end)
			}
		})
	}
}
func TestNoAudioProducesNoChunks(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent)}
	output := make(chan *bytes.Buffer, 4)
	startPacketer(t, track, output, Options{FlushInterval: 10 * time.Millisecond})
	select {
	case <-output:
		t.Fatal("headers sent as audio")
	case <-time.After(80 * time.Millisecond):
	}
}
func TestCancellationUnblocksFullQueue(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 1)}
	output := make(chan *bytes.Buffer)
	cancel, done := startPacketer(t, track, output, Options{FlushInterval: 10 * time.Millisecond, EnqueueTimeout: time.Minute})
	track.events <- readEvent{packet: packet(1)}
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("blocked queue ignored cancellation")
	}
}
func TestQueueTimeoutStopsProducer(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 1)}
	_, done := startPacketer(t, track, make(chan *bytes.Buffer), Options{FlushInterval: 10 * time.Millisecond, EnqueueTimeout: 20 * time.Millisecond})
	track.events <- readEvent{packet: packet(1)}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "enqueue timeout") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("full queue blocked forever")
	}
}
func TestReorderedReadyPacketsAreDrained(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 8)}
	output := make(chan *bytes.Buffer, 4)
	_, done := startPacketer(t, track, output, Options{FlushInterval: time.Hour})
	for _, seq := range []uint16{1, 3, 2, 4} {
		track.events <- readEvent{packet: packet(seq)}
	}
	close(track.events)
	chunk := getChunk(t, output).Bytes()
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	var previous int
	for _, seq := range []uint16{1, 2, 3, 4} {
		at := bytes.Index(chunk, packet(seq).Payload)
		if at < previous || at < 0 {
			t.Fatalf("packet %d missing or out of order", seq)
		}
		previous = at
	}
}

func TestPacketLossFollowedBySilenceDoesNotHoldTail(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 2)}
	output := make(chan *bytes.Buffer, 4)
	startPacketer(t, track, output, Options{FlushInterval: 30 * time.Millisecond})
	track.events <- readEvent{packet: packet(1)}
	track.events <- readEvent{packet: packet(3)} // Packet 2 never arrives.
	chunk := getChunk(t, output).Bytes()
	for _, seq := range []uint16{1, 3} {
		if !bytes.Contains(chunk, packet(seq).Payload) {
			t.Fatalf("packet %d held behind a missing packet during silence", seq)
		}
	}
}
func TestSizeLimitStillFlushesAndBuffersStayImmutable(t *testing.T) {
	track := &fakeTrack{events: make(chan readEvent, 8)}
	output := make(chan *bytes.Buffer, 8)
	startPacketer(t, track, output, Options{FlushInterval: time.Hour, MaxBufferBytes: 1})
	track.events <- readEvent{packet: packet(1)}
	first := getChunk(t, output)
	snapshot := append([]byte(nil), first.Bytes()...)
	track.events <- readEvent{packet: packet(2)}
	getChunk(t, output)
	if !bytes.Equal(snapshot, first.Bytes()) {
		t.Fatal("a delivered chunk was overwritten")
	}
}

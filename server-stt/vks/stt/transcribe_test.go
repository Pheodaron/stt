package stt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	sttpb "vks_back/gen/proto/stt"
)

type fakeSTT struct {
	ctx                  context.Context
	sent                 chan []byte
	responses            chan *sttpb.TranscribedText
	closeOnce            sync.Once
	closed               chan struct{}
	sendErr, recvErr     error
	blockSend, holdFinal bool
	waitForTwo           bool
	mu                   sync.Mutex
	sends                int
}

func (f *fakeSTT) Send(chunk *sttpb.VoiceChunk) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.blockSend {
		<-f.ctx.Done()
		return f.ctx.Err()
	}
	select {
	case <-f.ctx.Done():
		return f.ctx.Err()
	case f.sent <- append([]byte(nil), chunk.Segment...):
	}
	f.mu.Lock()
	f.sends++
	n := f.sends
	f.mu.Unlock()
	if f.waitForTwo && n == 2 {
		f.responses <- &sttpb.TranscribedText{}
		f.responses <- &sttpb.TranscribedText{}
		f.responses <- &sttpb.TranscribedText{}
	}
	return nil
}
func (f *fakeSTT) Recv() (*sttpb.TranscribedText, error) {
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	if f.waitForTwo {
		f.mu.Lock()
		ready := f.sends >= 2
		f.mu.Unlock()
		if !ready {
			select {
			case <-f.ctx.Done():
				return nil, f.ctx.Err()
			case response := <-f.responses:
				return response, nil
			}
		}
	}
	select {
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	case response := <-f.responses:
		return response, nil
	case <-f.closed:
		// Consume every queued partial/final before EOF.
		select {
		case response := <-f.responses:
			return response, nil
		default:
		}
		if f.holdFinal {
			<-f.ctx.Done()
			return nil, f.ctx.Err()
		}
		return nil, io.EOF
	}
}
func (f *fakeSTT) CloseSend() error { f.closeOnce.Do(func() { close(f.closed) }); return nil }
func newFakeSTT(ctx context.Context) *fakeSTT {
	return &fakeSTT{ctx: ctx, sent: make(chan []byte, 8), responses: make(chan *sttpb.TranscribedText, 8), closed: make(chan struct{})}
}
func runExchange(t *testing.T, ctx context.Context, cancel context.CancelFunc, ch <-chan *bytes.Buffer, stream *fakeSTT, publish func(*sttpb.TranscribedText) error, timeout time.Duration) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- exchangeChunksWithTimeout(ctx, cancel, ch, stream, publish, timeout); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("stream workers leaked")
		}
	})
	return done
}
func awaitExchange(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("stream did not stop")
		return nil
	}
}
func TestSenderDoesNotWaitForOneResponsePerChunk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeSTT(ctx)
	stream.waitForTwo = true
	ch := make(chan *bytes.Buffer, 2)
	ch <- bytes.NewBufferString("first")
	ch <- bytes.NewBufferString("second")
	close(ch)
	count := 0
	done := runExchange(t, ctx, cancel, ch, stream, func(*sttpb.TranscribedText) error { count++; return nil }, time.Second)
	if err := awaitExchange(t, done); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("received %d responses, want 3 for two input chunks", count)
	}
	if len(stream.sent) != 2 {
		t.Fatal("not all chunks sent")
	}
	select {
	case <-stream.closed:
	default:
		t.Fatal("CloseSend was not called")
	}
}
func TestWorkerFailuresCancelOtherWorker(t *testing.T) {
	failure := errors.New("STT unavailable")
	for _, worker := range []string{"sender", "receiver", "publisher"} {
		t.Run(worker, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			stream := newFakeSTT(ctx)
			ch := make(chan *bytes.Buffer, 1)
			publish := func(*sttpb.TranscribedText) error { return nil }
			switch worker {
			case "sender":
				stream.sendErr = failure
				ch <- bytes.NewBufferString("audio")
			case "receiver":
				stream.recvErr = failure
				stream.blockSend = true
				ch <- bytes.NewBufferString("audio")
			case "publisher":
				stream.responses <- &sttpb.TranscribedText{}
				publish = func(*sttpb.TranscribedText) error { return failure }
			}
			done := runExchange(t, ctx, cancel, ch, stream, publish, time.Second)
			if err := awaitExchange(t, done); !errors.Is(err, failure) {
				t.Fatalf("got %v", err)
			}
			if ctx.Err() == nil {
				t.Fatal("RPC context not canceled")
			}
		})
	}
}
func TestFinalResponseTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeSTT(ctx)
	stream.holdFinal = true
	ch := make(chan *bytes.Buffer)
	close(ch)
	done := runExchange(t, ctx, cancel, ch, stream, func(*sttpb.TranscribedText) error { return nil }, 20*time.Millisecond)
	if err := awaitExchange(t, done); err == nil || !strings.Contains(err.Error(), "final response timeout") {
		t.Fatalf("got %v", err)
	}
}
func TestCanceledRPCStopsIdleSenderAndReceiver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeSTT(ctx)
	done := runExchange(t, ctx, cancel, make(chan *bytes.Buffer), stream, func(*sttpb.TranscribedText) error { return nil }, time.Second)
	cancel()
	if err := awaitExchange(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
func TestPrematureServerEOFIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeSTT(ctx)
	stream.recvErr = io.EOF
	done := runExchange(t, ctx, cancel, make(chan *bytes.Buffer), stream, func(*sttpb.TranscribedText) error { return nil }, time.Second)
	if err := awaitExchange(t, done); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v", err)
	}
}

package meeting

import (
	"context"
	"errors"
	"testing"
	"time"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/diarization"
)

func TestShutdownWaitsForNonInterruptibleInference(t *testing.T) {
	s := testService(t, audio.SampleRate, []diarization.Segment{interval(0, 1, 1)})
	started, unblock := make(chan struct{}), make(chan struct{})
	s.Model = modelFunc(func(context.Context, []byte) (string, error) { close(started); <-unblock; return "late result", nil })
	ctx, release, err := s.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.NewUpload(6)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := u.Write(ctx, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := u.Write(ctx, []byte("def")); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { defer release(); defer u.Close(); _, err := u.Finish(ctx); finished <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("inference did not start")
	}
	s.Stop()
	if ctx.Err() == nil {
		t.Fatal("active context was not cancelled")
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned during native inference")
	default:
	}
	close(unblock)
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("inference did not finish")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
}

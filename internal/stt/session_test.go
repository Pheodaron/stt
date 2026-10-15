package stt

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"vks-stt-v2/internal/audio"
)

func chunk(flags string, value byte, ms int) audio.Chunk {
	return audio.Chunk{
		PCM: bytes.Repeat([]byte{value}, len(flags)*audio.SampleRate*ms/1000*2),
		Map: flags,
	}
}

func TestBufferRolloverAndFrameDuration(t *testing.T) {
	for _, ms := range []int{10, 20, 30} {
		b := NewBuffer(2, ms, 25)
		first, last, next := chunk("1111111111", 1, ms), chunk("1111111110", 2, ms), chunk("1111111111", 3, ms)
		b.Add(first)
		b.Add(last)
		want := len(first.PCM) + 9*audio.SampleRate*ms/1000*2
		if !b.Full() || len(b.Sample()) != want {
			t.Fatalf("%d ms: %d != %d", ms, len(b.Sample()), want)
		}
		b.Add(next)
		want = audio.SampleRate*ms/1000*2 + len(next.PCM)
		if !b.Full() || len(b.Sample()) != want || b.Sample()[0] != 2 {
			t.Fatalf("rollover %d: %d", ms, len(b.Sample()))
		}
		b.Clear()
		if b.Full() || len(b.Sample()) != 0 {
			t.Fatal("clear failed")
		}
	}
}

func TestBufferSizeOneAndDurationCap(t *testing.T) {
	b := NewBuffer(1, 10, 1)
	for i := 0; i < 20; i++ {
		b.Add(chunk("111", byte(i), 10))
		if len(b.Sample()) != 960 || !b.Full() {
			t.Fatal("size=1 grows")
		}
	}
	b = NewBuffer(64, 10, 1)
	for i := 0; i < 100; i++ {
		b.Add(chunk("111", byte(i), 10))
		if len(b.Sample()) > 32000 {
			t.Fatal("duration cap exceeded")
		}
	}
}

type fakeProcessor struct{}

func (fakeProcessor) Process(ctx context.Context, data []byte) (audio.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return audio.Chunk{}, err
	}

	if len(data) == 0 {
		return audio.Chunk{}, nil
	}

	return chunk("111", data[0], 10), nil
}

type fakeModel struct{ err error }

func (m fakeModel) Transcribe(context.Context, []byte) (string, error) {
	return "Полный текст, а не первый символ.", m.err
}

func TestSessionFinalPartialFlushAndIsolation(t *testing.T) {
	s := &Service{
		Processor:  fakeProcessor{},
		Model:      fakeModel{},
		BufferSize: 2,
		FrameMS:    10,
		MaxSeconds: 25,
		Timeout:    time.Second,
	}
	a, b := s.NewSession(), s.NewSession()
	r, err := a.Transcribe(context.Background(), []byte{1}, false)
	if err != nil || r.Partial == nil || *r.Partial != "Полный текст, а не первый символ." {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = b.Transcribe(context.Background(), []byte{2}, false)
	if r.Partial == nil {
		t.Fatal("sessions share buffer")
	}
	r, _ = a.Transcribe(context.Background(), []byte{3}, false)
	if r.Message == nil || r.Partial != nil {
		t.Fatal("full window did not commit")
	}

	if a.Flush() != nil {
		t.Fatal("duplicate final")
	}

	if final := b.Flush(); final == nil || final.Message == nil {
		t.Fatal("last partial lost")
	}

	if b.Flush() != nil {
		t.Fatal("flush is not idempotent")
	}
	r, _ = b.Transcribe(context.Background(), nil, false)
	if r.Message == nil || *r.Message != "" {
		t.Fatal("silence compatibility")
	}
	r, _ = b.Transcribe(context.Background(), []byte{1}, true)
	if r.Message == nil || b.pending != nil {
		t.Fatal("unbuffered changes buffer")
	}
}

func TestInferenceFailureResetsSession(t *testing.T) {
	s := &Service{
		Processor:  fakeProcessor{},
		Model:      fakeModel{err: errors.New("model failure")},
		BufferSize: 2,
		FrameMS:    10,
		MaxSeconds: 25,
		Timeout:    time.Second,
	}
	session := s.NewSession()
	if _, err := session.Transcribe(context.Background(), []byte{1}, false); err == nil {
		t.Fatal("error suppressed")
	}

	if len(session.buffer.Sample()) != 0 || session.Flush() != nil {
		t.Fatal("failed audio retained")
	}
}

func TestCapacityWaitHonorsDeadline(t *testing.T) {
	s := &Service{
		Processor:  fakeProcessor{},
		Model:      fakeModel{},
		BufferSize: 2,
		FrameMS:    10,
		MaxSeconds: 25,
		Timeout:    10 * time.Millisecond,
		Slots:      make(chan struct{}, 1),
	}
	s.Slots <- struct{}{}
	if _, err := s.NewSession().Transcribe(context.Background(), []byte{1}, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait ignored deadline: %v", err)
	}
	<-s.Slots
}

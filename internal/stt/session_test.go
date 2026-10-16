package stt

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"vks-stt-v2/internal/audio"
)

func TestBufferPreservesEndsAndOverlap(t *testing.T) {
	b := NewBuffer(2, 25, 500)
	first := bytes.Repeat([]byte{1}, 20000)
	last := bytes.Repeat([]byte{2}, 24002) // Includes a partial 10/32 ms frame.
	b.Add(first)
	b.Add(last)
	want := append(append([]byte(nil), first...), last...)
	if !b.Full() || !bytes.Equal(b.Sample(), want) {
		t.Fatal("buffer cut the end of an uncommitted phrase")
	}
	b.Commit()
	next := bytes.Repeat([]byte{3}, 8000)
	b.Add(next)
	want = append(bytes.Repeat([]byte{2}, 16000), next...)
	if b.Full() || !bytes.Equal(b.Sample(), want) {
		t.Fatal("rollover did not retain exactly 500 ms of context")
	}
}

func TestBufferSealsBeforeDurationLimit(t *testing.T) {
	b := NewBuffer(64, 1, 100)
	first := bytes.Repeat([]byte{1}, 24000)
	second := bytes.Repeat([]byte{2}, 16000)
	b.Add(first)
	sealed := b.Add(second)
	if !bytes.Equal(sealed, first) || len(b.Sample()) != 19200 {
		t.Fatal("duration limit discarded uncommitted audio")
	}
	if !bytes.Equal(b.Sample()[3200:], second) {
		t.Fatal("new audio was truncated")
	}
	b.Commit()
	fullUpload := bytes.Repeat([]byte{3}, 32000)
	b.Add(fullUpload)
	if !bytes.Equal(b.Sample(), fullUpload) {
		t.Fatal("overlap displaced new audio")
	}
}

type fakeDecoder struct{}

func (fakeDecoder) Decode(ctx context.Context, data []byte) ([]byte, error) {
	if string(data) == "bad" {
		return nil, audio.ErrAudio
	}
	return append([]byte(nil), data...), ctx.Err()
}

type fakeVAD struct {
	streams []*fakeFilter
	tail    []byte
}

func (v *fakeVAD) NewStream() (audio.Filter, error) {
	f := &fakeFilter{tail: v.tail}
	v.streams = append(v.streams, f)
	return f, nil
}

type fakeFilter struct {
	tail   []byte
	closed bool
}

func (f *fakeFilter) Accept(_ context.Context, pcm []byte) (audio.Chunk, error) {
	if string(pcm) == "pause" {
		return audio.Chunk{Ended: true}, nil
	}
	return audio.Chunk{PCM: pcm}, nil
}

func (f *fakeFilter) Flush(context.Context) (audio.Chunk, error) {
	return audio.Chunk{PCM: f.tail, Ended: true}, nil
}

func (f *fakeFilter) Close() { f.closed = true }

type recordingModel struct {
	inputs [][]byte
	err    error
}

func (m *recordingModel) Transcribe(_ context.Context, pcm []byte) (string, error) {
	m.inputs = append(m.inputs, append([]byte(nil), pcm...))
	return "Полный текст.", m.err
}

func testService() (*Service, *fakeVAD, *recordingModel) {
	vad := &fakeVAD{}
	model := &recordingModel{}
	s := &Service{
		Decoder: fakeDecoder{}, VAD: vad, Model: model,
		BufferSize: 2, OverlapMS: 500, MaxSeconds: 25, Timeout: time.Second,
	}
	return s, vad, model
}

func TestSessionIsolationFinalAndFlush(t *testing.T) {
	service, vad, _ := testService()
	a, b := service.NewSession(), service.NewSession()
	defer a.Close()
	defer b.Close()
	ctx := context.Background()
	for _, s := range []*Session{a, b} {
		r, err := s.Transcribe(ctx, []byte{1, 2}, false)
		if err != nil || r.Partial == nil {
			t.Fatalf("first chunk: %+v %v", r, err)
		}
	}
	if len(vad.streams) != 2 || a.filter == b.filter {
		t.Fatal("VAD state shared between sessions")
	}
	r, err := a.Transcribe(ctx, []byte{3, 4}, false)
	if err != nil || r.Message == nil {
		t.Fatalf("full window: %+v %v", r, err)
	}
	if r, err := a.Flush(ctx); err != nil || r != nil {
		t.Fatal("flush duplicated a committed window", r, err)
	}
	if r, err := b.Flush(ctx); err != nil || r == nil || r.Message == nil {
		t.Fatal("flush lost the last partial", r, err)
	}
	if r, err := b.Flush(ctx); err != nil || r != nil {
		t.Fatal("flush is not idempotent", r, err)
	}
	for _, f := range vad.streams {
		if !f.closed {
			t.Fatal("native VAD not released")
		}
	}
}

func TestFlushRecognizesDelayedAudioTail(t *testing.T) {
	service, vad, model := testService()
	vad.tail = []byte{3, 4}
	session := service.NewSession()
	ctx := context.Background()
	session.Transcribe(ctx, []byte{1, 2}, false)
	r, err := session.Flush(ctx)
	if err != nil || r == nil || r.Message == nil {
		t.Fatalf("%+v %v", r, err)
	}
	if len(model.inputs) != 2 || !bytes.Equal(model.inputs[1], []byte{1, 2, 3, 4}) {
		t.Fatal("flush returned stale text without recognizing the tail", model.inputs)
	}
}

func TestSilenceCommitsPendingPhrase(t *testing.T) {
	service, _, model := testService()
	session := service.NewSession()
	defer session.Close()
	ctx := context.Background()
	session.Transcribe(ctx, []byte{1, 2}, false)
	r, err := session.Transcribe(ctx, []byte("pause"), false)
	if err != nil || r.Message == nil || *r.Message != "Полный текст." || session.pending != nil {
		t.Fatalf("silence did not commit the pending phrase: %+v %v", r, err)
	}
	if len(model.inputs) != 1 || len(session.buffer.Sample()) != 0 {
		t.Fatal("silence re-ran recognition or retained a finished phrase")
	}
}

func TestUnbufferedUsesSeparateVADAndRecognizesTail(t *testing.T) {
	service, vad, model := testService()
	vad.tail = []byte{9, 10}
	session := service.NewSession()
	defer session.Close()
	ctx := context.Background()
	session.Transcribe(ctx, []byte{1, 2}, false)
	session.Transcribe(ctx, []byte{3, 4}, true)
	if len(vad.streams) != 2 || vad.streams[0].closed || !vad.streams[1].closed {
		t.Fatal("unbuffered request changed buffered VAD state")
	}
	if !bytes.Equal(model.inputs[1], []byte{3, 4, 9, 10}) || !bytes.Equal(session.buffer.Sample(), []byte{1, 2}) {
		t.Fatal("unbuffered tail missing or buffered audio changed")
	}
}

func TestInvalidAudioPreservesStateAndInferenceFailureReleasesIt(t *testing.T) {
	service, vad, model := testService()
	session := service.NewSession()
	defer session.Close()
	ctx := context.Background()
	session.Transcribe(ctx, []byte{1, 2}, false)
	if _, err := session.Transcribe(ctx, []byte("bad"), false); !errors.Is(err, audio.ErrAudio) {
		t.Fatal(err)
	}
	if vad.streams[0].closed || !bytes.Equal(session.buffer.Sample(), []byte{1, 2}) {
		t.Fatal("invalid upload destroyed previous audio")
	}
	model.err = errors.New("model failure")
	if _, err := session.Transcribe(ctx, []byte{3, 4}, false); err == nil {
		t.Fatal("inference error suppressed")
	}
	if !vad.streams[0].closed || session.filter != nil || session.pending != nil {
		t.Fatal("failed session retained native state")
	}
}

func TestSessionDurationLimitRecognizesAllAudio(t *testing.T) {
	service, _, model := testService()
	service.MaxSeconds, service.BufferSize, service.OverlapMS = 1, 64, 100
	session := service.NewSession()
	defer session.Close()
	ctx := context.Background()
	first := bytes.Repeat([]byte{1}, 24000)
	second := bytes.Repeat([]byte{2}, 16000)
	session.Transcribe(ctx, first, false)
	r, err := session.Transcribe(ctx, second, false)
	if err != nil || r.Message == nil || !bytes.Equal(model.inputs[1], first) || !bytes.Equal(model.inputs[2][3200:], second) {
		t.Fatal("duration rollover lost audio", err)
	}
	// VAD pre-roll may make a valid upload exceed the model window slightly.
	if _, err := session.Transcribe(ctx, bytes.Repeat([]byte{3}, 34000), false); err != nil {
		t.Fatal(err)
	}
	for _, pcm := range model.inputs {
		if len(pcm) > 32000 {
			t.Fatal("oversized window sent to GigaAM")
		}
	}
}

func TestCapacityWaitHonorsDeadline(t *testing.T) {
	service, _, _ := testService()
	service.Timeout = 10 * time.Millisecond
	service.Slots = make(chan struct{}, 1)
	service.Slots <- struct{}{}
	if _, err := service.NewSession().Transcribe(context.Background(), []byte{1, 2}, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait ignored deadline: %v", err)
	}
}

func TestEndpointClearsCommittedOverlap(t *testing.T) {
	service, _, model := testService()
	service.BufferSize = 1
	session := service.NewSession()
	defer session.Close()
	ctx := context.Background()
	session.Transcribe(ctx, []byte{1, 2}, false)
	session.Transcribe(ctx, []byte("pause"), false)
	session.Transcribe(ctx, []byte{3, 4}, false)
	if !bytes.Equal(model.inputs[1], []byte{3, 4}) {
		t.Fatal("finished phrase leaked into the next phrase")
	}
}

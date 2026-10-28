package meeting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/diarization"
)

type diarizerFunc func(context.Context, string) ([]diarization.Segment, error)

func (f diarizerFunc) Diarize(c context.Context, p string) ([]diarization.Segment, error) {
	return f(c, p)
}

type modelFunc func(context.Context, []byte) (string, error)

func (f modelFunc) Transcribe(c context.Context, p []byte) (string, error) { return f(c, p) }
func interval(a, b int64, s uint32) diarization.Segment {
	return diarization.Segment{Start: a * audio.SampleRate, End: b * audio.SampleRate, Speaker: s}
}

func testService(t *testing.T, samples int64, intervals []diarization.Segment) *Service {
	t.Helper()
	s := &Service{TempDir: t.TempDir(), ChunkBytes: 4, MaxSeconds: 25,
		Diarizer: diarizerFunc(func(context.Context, string) ([]diarization.Segment, error) {
			return append([]diarization.Segment(nil), intervals...), nil
		}),
		Model: modelFunc(func(_ context.Context, pcm []byte) (string, error) {
			if len(pcm) > 25*audio.SampleRate*2 {
				t.Error("ASR limit exceeded")
			}
			return "текст", nil
		}),
		Decode: func(_ context.Context, _, input, output string) (int64, error) {
			data, err := os.ReadFile(input)
			if err != nil {
				return 0, err
			}
			if string(data) != "abcdef" {
				t.Fatalf("upload reordered: %q", data)
			}
			return samples, os.WriteFile(output, make([]byte, samples*2), 0600)
		},
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func finishUpload(t *testing.T, s *Service) (*Result, error) {
	t.Helper()
	u, err := s.NewUpload(6)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for _, p := range []string{"ab", "c", "def"} {
		if err := u.Write(context.Background(), []byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	return u.Finish(context.Background())
}

func TestMeetingKeepsTimelineAndOverlapSpeakers(t *testing.T) {
	s := testService(t, 30*audio.SampleRate, []diarization.Segment{interval(10, 18, 1), interval(16, 21, 2)})
	r, err := finishUpload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Samples != 30*audio.SampleRate || !reflect.DeepEqual(r.Speakers, []uint32{1, 2}) {
		t.Fatalf("%+v", r)
	}
	if len(r.Segments) != 3 {
		t.Fatalf("%+v", r.Segments)
	}
	for i, want := range []Span{
		{Start: 10 * audio.SampleRate, End: 16 * audio.SampleRate, Speakers: []uint32{1}},
		{Start: 16 * audio.SampleRate, End: 18 * audio.SampleRate, Speakers: []uint32{1, 2}, Overlap: true},
		{Start: 18 * audio.SampleRate, End: 21 * audio.SampleRate, Speakers: []uint32{2}},
	} {
		if !reflect.DeepEqual(r.Segments[i].Span, want) {
			t.Fatalf("segment %d: %+v", i, r.Segments[i])
		}
	}
	entries, _ := filepath.Glob(filepath.Join(s.TempDir, "upload-*"))
	if len(entries) != 0 {
		t.Fatal("temporary files leaked")
	}
}
func TestShortInterruptionRecognizedOnceWithAllSpeakers(t *testing.T) {
	s := testService(t, 10*audio.SampleRate, []diarization.Segment{interval(1, 8, 1), {Start: 4 * audio.SampleRate, End: 4*audio.SampleRate + audio.SampleRate/2, Speaker: 2}})
	r, err := finishUpload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Segments) != 1 || !r.Segments[0].Overlap || !reflect.DeepEqual(r.Segments[0].Speakers, []uint32{1, 2}) {
		t.Fatalf("%+v", r.Segments)
	}
}
func TestLongTurnDoesNotLoseOrDuplicateCoreSamples(t *testing.T) {
	s := testService(t, 60*audio.SampleRate, []diarization.Segment{interval(2, 57, 1)})
	r, err := finishUpload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	var previous int64 = 2 * audio.SampleRate
	for _, v := range r.Segments {
		if v.Start != previous || v.End-v.Start > 25*audio.SampleRate {
			t.Fatalf("%+v", v)
		}
		previous = v.End
	}
	if previous != 57*audio.SampleRate || len(r.Segments) != 3 || !r.Segments[0].ForcedSplit {
		t.Fatalf("%+v", r.Segments)
	}
}

func TestLongTurnCutsAtPauseWithoutLosingTimeline(t *testing.T) {
	s := testService(t, 40*audio.SampleRate, []diarization.Segment{interval(2, 37, 1)})
	var pauseCalls int
	s.FindPause = func(_ context.Context, pcm []byte) (int, error) {
		pauseCalls++
		if len(pcm) != 2*audio.SampleRate*2 {
			t.Fatalf("unexpected pause search size: %d", len(pcm))
		}
		return audio.SampleRate, nil
	}
	r, err := finishUpload(t, s)
	if err != nil {
		t.Fatal(err)
	}
	// 2 s + (25 s - 400 ms of padding reserve) - 1 s before the limit.
	const cut = 2*audio.SampleRate + 25*audio.SampleRate - 2*audio.SampleRate/5 - audio.SampleRate
	if pauseCalls != 1 || len(r.Segments) != 2 || r.Segments[0].End != cut || r.Segments[1].Start != cut || r.Segments[0].ForcedSplit || r.Segments[1].End != 37*audio.SampleRate {
		t.Fatalf("pause cuts=%d, segments=%+v", pauseCalls, r.Segments)
	}
}
func TestSilenceAndInvalidNativeIntervals(t *testing.T) {
	s := testService(t, audio.SampleRate, nil)
	r, err := finishUpload(t, s)
	if err != nil || len(r.Segments) != 0 || len(r.Speakers) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, v := range []diarization.Segment{{Start: -1, End: 10, Speaker: 1}, {Start: 0, End: 10, Speaker: 9}, {Start: 0, End: audio.SampleRate + 1, Speaker: 1}} {
		if _, err := activity([]diarization.Segment{v}, audio.SampleRate); err == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
}
func TestAdmissionSurvivesCancellationUntilRelease(t *testing.T) {
	s := testService(t, audio.SampleRate, nil)
	ctx, cancel := context.WithCancel(context.Background())
	_, release, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, _, err := s.Begin(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	release()
	release()
	ctx2, release2, err := s.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.Stop()
	if ctx2.Err() == nil {
		t.Fatal("Stop did not cancel active meeting")
	}
	release2()
	if _, _, err := s.Begin(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestUploadRejectsTruncationOversizeAndCancellation(t *testing.T) {
	s := testService(t, audio.SampleRate, nil)
	u, err := s.NewUpload(6)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	if err := u.Write(context.Background(), []byte("abcde")); !errors.Is(err, ErrUpload) {
		t.Fatal(err)
	}
	if err := u.Write(context.Background(), nil); !errors.Is(err, ErrUpload) {
		t.Fatal(err)
	}
	if err := u.Write(context.Background(), []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Finish(context.Background()); !errors.Is(err, ErrUpload) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := u.Write(ctx, []byte("def")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestWorkspaceLockAndCrashCleanup(t *testing.T) {
	s := testService(t, audio.SampleRate, nil)
	other := &Service{TempDir: s.TempDir, ChunkBytes: s.ChunkBytes, MaxSeconds: s.MaxSeconds, Diarizer: s.Diarizer, Model: s.Model, Decode: s.Decode}
	if err := other.Init(); err == nil {
		other.Close()
		t.Fatal("workspace shared between processes")
	}
	s.Close()
	stale := filepath.Join(s.TempDir, "upload-crashed")
	if err := os.Mkdir(stale, 0700); err != nil {
		t.Fatal(err)
	}
	other.closed = false
	if err := other.Init(); err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("crash leftovers not removed")
	}
}

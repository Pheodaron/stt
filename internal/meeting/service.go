// Package meeting implements a synchronous, disk-backed meeting pipeline.
package meeting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/diarization"
)

var (
	ErrBusy   = errors.New("a meeting is already uploading or processing")
	ErrClosed = errors.New("meeting service is stopping")
	ErrUpload = errors.New("invalid meeting upload")
)

type Diarizer interface {
	Diarize(context.Context, string) ([]diarization.Segment, error)
}
type Model interface {
	Transcribe(context.Context, []byte) (string, error)
}
type DecodeFile func(context.Context, string, string, string) (int64, error)
type PauseFinder func(context.Context, []byte) (int, error)

type Transcript struct {
	Span
	Text        string
	ForcedSplit bool
}
type Result struct {
	Samples     int64
	Speakers    []uint32
	Diarization []diarization.Segment
	Segments    []Transcript
}

type Service struct {
	FFmpeg     string
	TempDir    string
	ChunkBytes int
	MaxSeconds int
	Diarizer   Diarizer
	Model      Model
	Decode     DecodeFile
	FindPause  PauseFinder
	mu         sync.Mutex
	closed     bool
	active     bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	lock       *os.File
}

// Init takes exclusive ownership of the workspace before removing crash leftovers.
// Two service processes must use different workspaces.
func (s *Service) Init() error {
	if s.ChunkBytes < 1 || s.MaxSeconds < 1 || s.MaxSeconds > 25 || s.Diarizer == nil || s.Model == nil {
		return fmt.Errorf("invalid meeting service configuration")
	}
	if s.Decode == nil {
		s.Decode = audio.DecodeFile
	}
	if err := os.MkdirAll(s.TempDir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.TempDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return fmt.Errorf("meeting workspace is in use: %w", err)
	}
	s.lock = f
	entries, err := os.ReadDir(s.TempDir)
	if err != nil {
		s.Close()
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "upload-") {
			if err := os.RemoveAll(filepath.Join(s.TempDir, entry.Name())); err != nil {
				s.Close()
				return err
			}
		}
	}
	return nil
}

// Begin is called before the first stream.Recv, so a second upload is rejected
// without reading its file. The permit outlives cancellation and native calls.
func (s *Service) Begin(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s.closed {
		return nil, nil, ErrClosed
	}
	if s.active {
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	s.active = true
	s.cancel = cancel
	s.wg.Add(1)
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			s.active = false
			s.cancel = nil
			s.mu.Unlock()
			s.wg.Done()
		})
	}
	return ctx, release, nil
}

func (s *Service) Stop() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}
func (s *Service) Close() {
	s.Stop()
	s.wg.Wait()
	if s.lock != nil {
		_ = unix.Flock(int(s.lock.Fd()), unix.LOCK_UN)
		_ = s.lock.Close()
		s.lock = nil
	}
}

type Upload struct {
	service            *Service
	dir                string
	file               *os.File
	expected, received uint64
}

func (s *Service) NewUpload(size uint64) (*Upload, error) {
	if size == 0 || size > math.MaxInt64 {
		return nil, fmt.Errorf("%w: invalid file size", ErrUpload)
	}
	dir, err := os.MkdirTemp(s.TempDir, "upload-")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "input"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &Upload{service: s, dir: dir, file: f, expected: size}, nil
}
func (u *Upload) Close() {
	if u.file != nil {
		_ = u.file.Close()
		u.file = nil
	}
	_ = os.RemoveAll(u.dir)
}
func (u *Upload) Write(ctx context.Context, p []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(p) == 0 || len(p) > u.service.ChunkBytes || uint64(len(p)) > u.expected-u.received {
		return fmt.Errorf("%w: invalid chunk size", ErrUpload)
	}
	n, err := u.file.Write(p)
	u.received += uint64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return err
}
func (u *Upload) Finish(ctx context.Context) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if u.received != u.expected {
		return nil, fmt.Errorf("%w: received %d bytes, expected %d", ErrUpload, u.received, u.expected)
	}
	if err := u.file.Close(); err != nil {
		return nil, err
	}
	u.file = nil
	return u.service.process(ctx, u.dir)
}

func (s *Service) process(ctx context.Context, dir string) (*Result, error) {
	started := time.Now()
	input, pcmPath := filepath.Join(dir, "input"), filepath.Join(dir, "audio.pcm")
	samples, err := s.Decode(ctx, s.FFmpeg, input, pcmPath)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(input); err != nil {
		return nil, err
	}
	slog.Info("meeting decoded", "audio_seconds", float64(samples)/audio.SampleRate, "elapsed", time.Since(started))
	started = time.Now()
	intervals, err := s.Diarizer.Diarize(ctx, pcmPath)
	if err != nil {
		return nil, err
	}
	base, err := activity(intervals, samples)
	if err != nil {
		return nil, err
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].Start == intervals[j].Start {
			return intervals[i].Speaker < intervals[j].Speaker
		}
		return intervals[i].Start < intervals[j].Start
	})
	result := &Result{Samples: samples, Diarization: intervals}
	for _, v := range base {
		result.Speakers = union(result.Speakers, v.Speakers)
	}
	slog.Info("meeting diarized", "speakers", len(result.Speakers), "intervals", len(intervals), "elapsed", time.Since(started))
	f, err := os.Open(pcmPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	maxSamples := int64(s.MaxSeconds * audio.SampleRate)
	padding := min(int64(audio.SampleRate/5), maxSamples/10)
	coreLimit := maxSamples - 2*padding
	var pieces []Transcript
	for _, v := range group(base, coreLimit) {
		for start := v.Start; start < v.End; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end := min(v.End, start+coreLimit)
			forced := false
			if end < v.End {
				cut, err := s.pause(ctx, f, max(start, end-2*audio.SampleRate), end)
				if err != nil {
					return nil, err
				}
				if cut > start {
					end = cut
				} else {
					forced = true
				}
			}
			ids, overlap := describe(base, start, end)
			pieces = append(pieces, Transcript{Span: Span{Start: start, End: end, Speakers: ids, Overlap: overlap}, ForcedSplit: forced})
			start = end
		}
	}
	started = time.Now()
	for i, v := range pieces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		left, right := int64(0), samples
		if i > 0 {
			left = pieces[i-1].End
		}
		if i+1 < len(pieces) {
			right = pieces[i+1].Start
		}
		start, end := max(left, v.Start-padding), min(right, v.End+padding)
		pcm, err := readPCM(f, start, end)
		if err != nil {
			return nil, err
		}
		text, err := s.Model.Transcribe(ctx, pcm)
		if err != nil {
			return nil, err
		}
		v.Text = text
		result.Segments = append(result.Segments, v)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slog.Info("meeting transcribed", "fragments", len(result.Segments), "elapsed", time.Since(started))
	return result, nil
}

func readPCM(f *os.File, start, end int64) ([]byte, error) {
	if start < 0 || end < start || end-start > 25*audio.SampleRate {
		return nil, fmt.Errorf("invalid ASR range")
	}
	pcm := make([]byte, int(end-start)*2)
	_, err := f.ReadAt(pcm, start*2)
	return pcm, err
}
func (s *Service) pause(ctx context.Context, f *os.File, start, end int64) (int64, error) {
	if s.FindPause == nil {
		return 0, nil
	}
	pcm, err := readPCM(f, start, end)
	if err != nil {
		return 0, err
	}
	cut, err := s.FindPause(ctx, pcm)
	if err != nil {
		return 0, err
	}
	if cut <= 0 || int64(cut) >= end-start {
		return 0, nil
	}
	return start + int64(cut), nil
}

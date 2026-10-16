package stt

import (
	"context"
	"strings"
	"time"

	"vks-stt-v2/internal/audio"
)

type Decoder interface {
	Decode(context.Context, []byte) ([]byte, error)
}

type VAD interface {
	NewStream() (audio.Filter, error)
}

type Model interface {
	Transcribe(context.Context, []byte) (string, error)
}

type Result struct {
	Partial *string `json:"partial,omitempty"`
	Message *string `json:"message,omitempty"`
}

type Service struct {
	Decoder    Decoder
	VAD        VAD
	Model      Model
	BufferSize int
	OverlapMS  int
	MaxSeconds int
	Timeout    time.Duration
	Slots      chan struct{}
}

type Session struct {
	service *Service
	buffer  *Buffer
	filter  audio.Filter
	pending *string
}

func (s *Service) NewSession() *Session {
	return &Session{
		service: s,
		buffer:  NewBuffer(s.BufferSize, s.MaxSeconds, s.OverlapMS),
	}
}

func (s *Session) Transcribe(ctx context.Context, data []byte, unbuffered bool) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.service.Timeout)
	defer cancel()
	release, err := s.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()

	// Invalid uploads leave the existing stream untouched.
	pcm, err := s.service.Decoder.Decode(ctx, data)
	if err != nil {
		return Result{}, err
	}
	if unbuffered {
		return s.transcribeIndependent(ctx, pcm)
	}
	if err := s.ensureFilter(); err != nil {
		return Result{}, err
	}
	chunk, err := s.filter.Accept(ctx, pcm)
	if err != nil {
		s.Close()
		return Result{}, err
	}
	return s.recognize(ctx, chunk)
}

func (s *Session) transcribeIndependent(ctx context.Context, pcm []byte) (Result, error) {
	filter, err := s.service.VAD.NewStream()
	if err != nil {
		return Result{}, err
	}
	defer filter.Close()
	chunk, err := filter.Accept(ctx, pcm)
	if err != nil {
		return Result{}, err
	}
	tail, err := filter.Flush(ctx)
	if err != nil {
		return Result{}, err
	}
	chunk.PCM = append(chunk.PCM, tail.PCM...)
	text, err := s.service.Model.Transcribe(ctx, chunk.PCM)
	if err != nil {
		return Result{}, err
	}
	return Result{Message: &text}, nil
}

func (s *Session) recognize(ctx context.Context, chunk audio.Chunk) (Result, error) {
	limit := s.service.MaxSeconds * audio.SampleRate * 2
	if len(chunk.PCM) <= limit {
		return s.recognizeWindow(ctx, chunk)
	}

	// Retained pre-roll can make a valid upload slightly longer than the ASR
	// limit. Recognize every sample in bounded windows instead of trimming it.
	var texts []string
	for len(chunk.PCM) != 0 {
		n := min(len(chunk.PCM), limit)
		part := audio.Chunk{PCM: chunk.PCM[:n], Ended: chunk.Ended && n == len(chunk.PCM)}
		result, err := s.recognizeWindow(ctx, part)
		if err != nil {
			return Result{}, err
		}
		if result.Message != nil {
			texts = append(texts, *result.Message)
		} else if result.Partial != nil {
			texts = append(texts, *result.Partial)
		}
		s.buffer.Commit()
		s.pending = nil
		chunk.PCM = chunk.PCM[n:]
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	return Result{Message: &text}, nil
}

func (s *Session) recognizeWindow(ctx context.Context, chunk audio.Chunk) (Result, error) {
	if len(chunk.PCM) == 0 {
		if chunk.Ended {
			text := s.pending
			s.pending = nil
			s.buffer.Clear()
			if text != nil {
				return Result{Message: text}, nil
			}
		}
		empty := ""
		return Result{Message: &empty}, nil
	}

	var completed string
	if sealed := s.buffer.Add(chunk.PCM); len(sealed) != 0 {
		// Commit the previous window before the duration limit. No raw audio is
		// dropped, even when the caller uploads a large chunk after a partial.
		var err error
		completed, err = s.service.Model.Transcribe(ctx, sealed)
		if err != nil {
			s.Close()
			return Result{}, err
		}
	}
	text, err := s.service.Model.Transcribe(ctx, s.buffer.Sample())
	if err != nil {
		s.Close()
		return Result{}, err
	}
	if completed != "" {
		text = strings.TrimSpace(completed + "\n" + text)
	}
	if s.buffer.Full() || chunk.Ended || completed != "" {
		s.pending = nil
		if chunk.Ended {
			s.buffer.Clear()
		} else {
			s.buffer.Commit()
		}
		return Result{Message: &text}, nil
	}
	s.pending = &text
	return Result{Partial: &text}, nil
}

// Flush recognizes real samples still waiting for a complete VAD window before
// committing the final text. It is called on gRPC half-close or explicit WS flush.
func (s *Session) Flush(ctx context.Context) (*Result, error) {
	defer s.Close()
	ctx, cancel := context.WithTimeout(ctx, s.service.Timeout)
	defer cancel()
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if s.filter == nil {
		return nil, nil
	}
	tail, err := s.filter.Flush(ctx)
	if err != nil {
		return nil, err
	}
	if len(tail.PCM) != 0 {
		result, err := s.recognize(ctx, tail)
		return &result, err
	}
	if s.pending == nil {
		return nil, nil
	}
	return &Result{Message: s.pending}, nil
}

func (s *Session) ensureFilter() error {
	if s.filter != nil {
		return nil
	}
	var err error
	s.filter, err = s.service.VAD.NewStream()
	return err
}

func (s *Session) acquire(ctx context.Context) (func(), error) {
	if s.service.Slots == nil {
		return func() {}, nil
	}
	select {
	case s.service.Slots <- struct{}{}:
		return func() { <-s.service.Slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close releases native VAD resources; transports call it on every exit path.
func (s *Session) Close() {
	if s.filter != nil {
		s.filter.Close()
		s.filter = nil
	}
	s.buffer.Clear()
	s.pending = nil
}

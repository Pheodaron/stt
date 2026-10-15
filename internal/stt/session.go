package stt

import (
	"context"
	"time"

	"vks-stt-v2/internal/audio"
)

type Processor interface {
	Process(context.Context, []byte) (audio.Chunk, error)
}

type Model interface {
	Transcribe(context.Context, []byte) (string, error)
}

type Result struct {
	Partial *string `json:"partial,omitempty"`
	Message *string `json:"message,omitempty"`
}

type Service struct {
	Processor  Processor
	Model      Model
	BufferSize int
	FrameMS    int
	MaxSeconds int
	Timeout    time.Duration
	Slots      chan struct{}
}

type Session struct {
	service *Service
	buffer  *Buffer
	pending *string
}

func (s *Service) NewSession() *Session {
	return &Session{
		service: s,
		buffer:  NewBuffer(s.BufferSize, s.FrameMS, s.MaxSeconds),
	}
}

func (s *Session) Transcribe(ctx context.Context, data []byte, unbuffered bool) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.service.Timeout)
	defer cancel()
	if s.service.Slots != nil {
		select {
		case s.service.Slots <- struct{}{}:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		defer func() { <-s.service.Slots }()
	}

	chunk, err := s.service.Processor.Process(ctx, data)
	if err != nil {
		return Result{}, err
	}

	if len(chunk.PCM) == 0 {
		empty := ""
		return Result{Message: &empty}, nil
	}

	pcm := chunk.PCM
	if !unbuffered {
		s.buffer.Add(chunk)
		pcm = s.buffer.Sample()
	}

	text, err := s.service.Model.Transcribe(ctx, pcm)
	if err != nil {
		if !unbuffered {
			s.buffer.Clear()
			s.pending = nil
		}
		return Result{}, err
	}

	if unbuffered {
		return Result{Message: &text}, nil
	}

	if s.buffer.Full() {
		s.pending = nil
		return Result{Message: &text}, nil
	}

	s.pending = &text
	return Result{Partial: &text}, nil
}

// Flush commits the last partial when the gRPC client half-closes or calls WS flush.
func (s *Session) Flush() *Result {
	defer s.reset()

	if s.pending == nil {
		return nil
	}

	return &Result{Message: s.pending}
}

func (s *Session) reset() {
	s.buffer.Clear()
	s.pending = nil
}

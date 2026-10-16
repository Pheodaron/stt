package stt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync/atomic"
	"time"

	sttpb "vks_back/gen/proto/stt"
)

const transcriptDrainTimeout = 10 * time.Second

type transcribeStream interface {
	Send(*sttpb.VoiceChunk) error
	Recv() (*sttpb.TranscribedText, error)
	CloseSend() error
}

type streamResult struct {
	sender bool
	err    error
}

// cancel must cancel the RPC itself, not just a child of the RPC's context.
// One sender and one receiver are allowed concurrently by gRPC.
func exchangeChunks(ctx context.Context, cancel context.CancelFunc, chunks <-chan *bytes.Buffer,
	stream transcribeStream, publish func(*sttpb.TranscribedText) error,
) error {
	return exchangeChunksWithTimeout(ctx, cancel, chunks, stream, publish, transcriptDrainTimeout)
}

func exchangeChunksWithTimeout(ctx context.Context, cancel context.CancelFunc, chunks <-chan *bytes.Buffer,
	stream transcribeStream, publish func(*sttpb.TranscribedText) error, drainTimeout time.Duration,
) error {
	defer cancel()
	results := make(chan streamResult, 2)
	var closing atomic.Bool
	go func() {
		err := func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case chunk, ok := <-chunks:
					if !ok {
						closing.Store(true)
						return stream.CloseSend()
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := stream.Send(&sttpb.VoiceChunk{Segment: chunk.Bytes()}); err != nil {
						return fmt.Errorf("STT send: %w", err)
					}
					log.Println("Sent:", chunk.Len())
				}
			}
		}()
		results <- streamResult{sender: true, err: err}
	}()
	go func() {
		err := func() error {
			for {
				res, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					if !closing.Load() {
						return fmt.Errorf("STT closed before input finished: %w", io.ErrUnexpectedEOF)
					}
					return nil
				}
				if err != nil {
					return fmt.Errorf("STT receive: %w", err)
				}
				if err := publish(res); err != nil {
					return fmt.Errorf("publish transcript: %w", err)
				}
			}
		}()
		results <- streamResult{err: err}
	}()

	var firstErr error
	var timer *time.Timer
	var timeout <-chan time.Time
	ctxDone := ctx.Done()
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for finished := 0; finished < 2; {
		select {
		case result := <-results:
			finished++
			if result.err != nil && firstErr == nil {
				firstErr = result.err
				cancel()
			}
			if result.sender && result.err == nil {
				timer = time.NewTimer(drainTimeout)
				timeout = timer.C
			}
		case <-ctxDone:
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			ctxDone = nil
			cancel()
		case <-timeout:
			if firstErr == nil {
				firstErr = errors.New("STT final response timeout")
			}
			timeout = nil
			cancel()
		}
	}
	return firstErr
}

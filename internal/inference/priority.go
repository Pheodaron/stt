// Package inference shares ASR capacity between live and recorded audio.
package inference

import (
	"context"
	"sync"
)

type Model interface {
	Transcribe(context.Context, []byte) (string, error)
}

// Priority admits live callers before the next batch fragment. It never
// interrupts a running native call and needs no background worker.
type Priority struct {
	model                         Model
	mu                            sync.Mutex
	changed                       chan struct{}
	capacity, active, liveWaiting int
}

func NewPriority(model Model, capacity int) *Priority {
	if capacity < 1 {
		panic("ASR capacity must be positive")
	}
	return &Priority{model: model, capacity: capacity, changed: make(chan struct{})}
}

func (p *Priority) signal() { close(p.changed); p.changed = make(chan struct{}) }

func (p *Priority) acquire(ctx context.Context, live bool) (func(), error) {
	p.mu.Lock()
	if live {
		p.liveWaiting++
	}
	for {
		if err := ctx.Err(); err != nil {
			if live {
				p.liveWaiting--
				p.signal()
			}
			p.mu.Unlock()
			return nil, err
		}
		if p.active < p.capacity && (live || p.liveWaiting == 0) {
			break
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		p.mu.Lock()
	}
	if live {
		p.liveWaiting--
	}
	p.active++
	p.mu.Unlock()
	return func() { p.mu.Lock(); p.active--; p.signal(); p.mu.Unlock() }, nil
}

func (p *Priority) transcribe(ctx context.Context, pcm []byte, live bool) (string, error) {
	release, err := p.acquire(ctx, live)
	if err != nil {
		return "", err
	}
	defer release()
	return p.model.Transcribe(ctx, pcm)
}

func (p *Priority) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	return p.transcribe(ctx, pcm, true)
}

type batch struct{ priority *Priority }

func (b batch) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	return b.priority.transcribe(ctx, pcm, false)
}
func (p *Priority) Batch() Model { return batch{p} }

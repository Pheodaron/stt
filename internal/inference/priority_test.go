package inference

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type blockingModel struct {
	started chan string
	release chan struct{}
}

func (m *blockingModel) Transcribe(_ context.Context, p []byte) (string, error) {
	m.started <- string(p)
	<-m.release
	return string(p), nil
}

func TestLiveWaiterRunsBeforeNextBatchFragment(t *testing.T) {
	m := &blockingModel{started: make(chan string, 3), release: make(chan struct{}, 3)}
	p := NewPriority(m, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	call := func(model Model, name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := model.Transcribe(ctx, []byte(name)); err != nil {
				t.Error(err)
			}
		}()
	}
	call(p.Batch(), "batch1")
	if got := <-m.started; got != "batch1" {
		t.Fatal(got)
	}
	call(p.Batch(), "batch2")
	call(p, "live")
	for {
		p.mu.Lock()
		waiting := p.liveWaiting
		p.mu.Unlock()
		if waiting == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("live call did not enter scheduler")
		case <-time.After(time.Millisecond):
		}
	}
	m.release <- struct{}{}
	select {
	case got := <-m.started:
		if got != "live" {
			t.Fatalf("next call: %s", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	m.release <- struct{}{}
	select {
	case got := <-m.started:
		if got != "batch2" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	m.release <- struct{}{}
	wg.Wait()
}
func TestPriorityCancellationAndParallelCapacity(t *testing.T) {
	p := NewPriority(nil, 2)
	a, err := p.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.acquire(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.acquire(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.mu.Lock()
	waiting := p.liveWaiting
	active := p.active
	p.mu.Unlock()
	if waiting != 0 || active != 2 {
		t.Fatalf("waiting=%d active=%d", waiting, active)
	}
	a()
	b()
	c, err := p.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	c()
}

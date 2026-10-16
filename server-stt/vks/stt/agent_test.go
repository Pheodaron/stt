package stt

import (
	"context"
	"testing"
)

func TestLocalRemovalCancelsTrackContextsWithoutDisconnectCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trackContext, cancelTrack := context.WithCancel(ctx)
	defer cancelTrack()
	agent := &SttAgent{ctx: ctx, cancel: cancel}
	agent.Remove()
	if trackContext.Err() != context.Canceled {
		t.Fatal("track RPC still active after local removal")
	}
}

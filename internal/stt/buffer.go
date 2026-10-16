package stt

import "vks-stt-v2/internal/audio"

// Buffer never cuts an uncommitted phrase or removes its trailing samples.
// Committed audio contributes a short overlap to the next recognition window.
type Buffer struct {
	pcm       []byte
	size      int
	count     int
	maxBytes  int
	overlap   int
	committed bool
}

func NewBuffer(size, maxSeconds, overlapMS int) *Buffer {
	return &Buffer{
		size:     size,
		maxBytes: maxSeconds * audio.SampleRate * 2,
		overlap:  overlapMS * audio.SampleRate / 1000 * 2,
	}
}

// Add returns a window that must be recognized and committed before the new
// audio. A duration limit must never silently discard uncommitted samples.
func (b *Buffer) Add(pcm []byte) (sealed []byte) {
	if b.committed {
		b.retainOverlap(len(pcm))
	} else if len(b.pcm)+len(pcm) > b.maxBytes {
		sealed = b.pcm
		b.retainOverlap(len(pcm))
	}
	b.pcm = append(b.pcm, pcm...)
	b.count++
	return sealed
}

func (b *Buffer) retainOverlap(incoming int) {
	keep := min(len(b.pcm), b.overlap, max(0, b.maxBytes-incoming))
	b.pcm = append([]byte(nil), b.pcm[len(b.pcm)-keep:]...)
	b.count = 0
	b.committed = false
}

func (b *Buffer) Sample() []byte { return b.pcm }
func (b *Buffer) Full() bool     { return b.count >= b.size }
func (b *Buffer) Commit()        { b.committed = true }

func (b *Buffer) Clear() {
	b.pcm = nil
	b.count = 0
	b.committed = false
}

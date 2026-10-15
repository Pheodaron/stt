package stt

import (
	"strings"

	"vks-stt-v2/internal/audio"
)

// Buffer retains the tail of the last committed chunk as context for the next window.
type Buffer struct {
	chunks     []audio.Chunk
	size       int
	frameBytes int
	maxBytes   int
}

func NewBuffer(size, frameMS, maxSeconds int) *Buffer {
	return &Buffer{
		size:       size,
		frameBytes: audio.SampleRate * frameMS / 1000 * 2,
		maxBytes:   maxSeconds * audio.SampleRate * 2,
	}
}

func (b *Buffer) Add(chunk audio.Chunk) {
	if b.Full() {
		b.retainTail()
	}

	b.chunks = append(b.chunks, audio.Chunk{
		PCM: append([]byte(nil), chunk.PCM...),
		Map: chunk.Map,
	})
	b.trimToLimit()
}

func (b *Buffer) retainTail() {
	last := b.chunks[len(b.chunks)-1]
	if position := boundary(last.Map); position >= 0 {
		last.PCM = last.PCM[position*b.frameBytes:]
		last.Map = last.Map[position:]
	}

	b.chunks = nil
	if b.size > 1 {
		b.chunks = append(b.chunks, last)
	}
}

// Keep the sliding window within the model's maximum audio duration.
func (b *Buffer) trimToLimit() {
	total := 0
	for _, c := range b.chunks {
		total += len(c.PCM)
	}

	for total > b.maxBytes && len(b.chunks) > 0 {
		excess := total - b.maxBytes
		first := &b.chunks[0]
		if len(first.PCM) <= excess {
			total -= len(first.PCM)
			b.chunks = b.chunks[1:]
			continue
		}

		frames := (excess + b.frameBytes - 1) / b.frameBytes
		cut := frames * b.frameBytes
		first.PCM, first.Map = first.PCM[cut:], first.Map[frames:]
		total -= cut
	}
}

func (b *Buffer) Sample() []byte {
	var result []byte
	for i, c := range b.chunks {
		end := len(c.PCM)
		if i == len(b.chunks)-1 {
			if pos := boundary(c.Map); pos >= 0 {
				end = pos * b.frameBytes
			}
		}
		result = append(result, c.PCM[:end]...)
	}

	return result
}

func (b *Buffer) Full() bool {
	return len(b.chunks) >= b.size
}

func (b *Buffer) Clear() {
	b.chunks = nil
}

func boundary(m string) int {
	pos := strings.LastIndexByte(m, '0')
	if len(m) > 0 && pos*5 > len(m)*4 {
		return pos
	}

	return -1
}

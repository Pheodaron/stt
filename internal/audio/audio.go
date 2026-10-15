package audio

import (
	"context"
	"encoding/binary"
	"errors"
)

const SampleRate = 16000

var (
	ErrAudio = errors.New("invalid audio")
	ErrLimit = errors.New("audio limit exceeded")
)

type Chunk struct {
	PCM []byte
	Map string
}

type Processor struct {
	FFmpeg     string
	FrameMS    int
	PaddingMS  int
	VADMode    int
	MaxBytes   int
	MaxSeconds int
}

func (p Processor) Process(ctx context.Context, data []byte) (Chunk, error) {
	pcm, err := p.Decode(ctx, data)
	if err != nil {
		return Chunk{}, err
	}

	return Filter(pcm, p.FrameMS, p.PaddingMS, p.VADMode)
}

func Samples(pcm []byte) []float32 {
	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
	}

	return samples
}

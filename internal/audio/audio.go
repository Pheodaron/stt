package audio

import (
	"encoding/binary"
	"errors"
)

const SampleRate = 16000

var (
	ErrAudio = errors.New("invalid audio")
	ErrLimit = errors.New("audio limit exceeded")
)

type Chunk struct {
	PCM   []byte
	Ended bool
}

type Processor struct {
	FFmpeg     string
	MaxBytes   int
	MaxSeconds int
}

func Samples(pcm []byte) []float32 {
	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
	}

	return samples
}

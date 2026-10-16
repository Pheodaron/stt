package audio

import (
	"context"
	"fmt"
	"math"
	"os"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

const vadWindowSamples = 512 // Silero v6: 32 ms at 16 kHz.

// Filter belongs to one audio stream. It must not be shared between clients.
type Filter interface {
	Accept(context.Context, []byte) (Chunk, error)
	Flush(context.Context) (Chunk, error)
	Close()
}

type VADConfig struct {
	Model        string
	Threshold    float32
	PaddingMS    int
	MinSilenceMS int
}

type Silero struct {
	config VADConfig
}

func NewSilero(config VADConfig) (*Silero, error) {
	info, err := os.Stat(config.Model)
	if err != nil {
		return nil, fmt.Errorf("Silero VAD model %q: %w; rebuild the image and check VAD_MODEL_PATH", config.Model, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("invalid Silero VAD model %q", config.Model)
	}
	if math.IsNaN(float64(config.Threshold)) || config.Threshold < 0.01 || config.Threshold >= 1 {
		return nil, fmt.Errorf("Silero VAD threshold must be in [0.01, 1)")
	}
	if config.PaddingMS < 64 || config.PaddingMS > 2000 || config.MinSilenceMS < config.PaddingMS || config.MinSilenceMS > 3000 {
		return nil, fmt.Errorf("invalid Silero VAD settings")
	}

	vad := &Silero{config: config}
	// Fail before announcing readiness, rather than on the first audio request.
	stream, err := vad.NewStream()
	if err != nil {
		return nil, err
	}
	stream.Close()
	return vad, nil
}

func (v *Silero) NewStream() (Filter, error) {
	config := sherpa.VadModelConfig{
		SampleRate: SampleRate,
		NumThreads: 1,
		Provider:   "cpu",
		SileroVad: sherpa.SileroVadModelConfig{
			Model:              v.config.Model,
			Threshold:          v.config.Threshold,
			MinSpeechDuration:  0.01,
			MinSilenceDuration: float32(v.config.MinSilenceMS) / 1000,
			WindowSize:         vadWindowSamples,
			MaxSpeechDuration:  60,
		},
	}
	detector := sherpa.NewVoiceActivityDetector(&config, 2)
	if detector == nil {
		return nil, fmt.Errorf("sherpa-onnx failed to load Silero VAD")
	}
	return &streamFilter{
		detector: &sileroDetector{vad: detector},
		padding:  v.config.PaddingMS * SampleRate / 1000 * 2,
	}, nil
}

type speechDetector interface {
	Speech([]float32) bool
	Reset()
	Close()
}

type sileroDetector struct {
	vad *sherpa.VoiceActivityDetector
}

func (d *sileroDetector) Speech(samples []float32) bool {
	d.vad.AcceptWaveform(samples)
	speech := d.vad.IsSpeech()
	// PCM is collected in Go. Drain sherpa's segment collector to keep its
	// memory bounded even during continuous speech. Flush preserves the model's
	// recurrent state and speech/silence timers; only Reset clears those.
	d.vad.Flush()
	d.vad.Clear()
	return speech
}

func (d *sileroDetector) Reset() { d.vad.Reset() }
func (d *sileroDetector) Close() { sherpa.DeleteVoiceActivityDetector(d.vad) }

type streamFilter struct {
	detector speechDetector
	padding  int
	prefix   []byte
	pending  []byte
	sent     int // Prefix of pending already returned while speech was active.
	active   bool
}

func (f *streamFilter) Accept(ctx context.Context, pcm []byte) (Chunk, error) {
	if len(pcm)%2 != 0 {
		return Chunk{}, ErrAudio
	}
	if err := ctx.Err(); err != nil {
		return Chunk{}, err
	}
	f.pending = append(f.pending, pcm...)
	var chunk Chunk
	const windowBytes = vadWindowSamples * 2
	for len(f.pending) >= windowBytes {
		if err := ctx.Err(); err != nil {
			return Chunk{}, err
		}
		frame := f.pending[:windowBytes]
		f.collect(&chunk, frame[f.sent:], f.detector.Speech(Samples(frame)))
		f.pending = f.pending[windowBytes:]
		f.sent = 0
	}

	// Do not defer the last <32 ms of an active phrase until the next request:
	// the current request may be the last one before a window is committed.
	if f.active {
		chunk.PCM = append(chunk.PCM, f.pending[f.sent:]...)
		f.sent = len(f.pending)
	}
	return chunk, nil
}

func (f *streamFilter) collect(chunk *Chunk, pcm []byte, speech bool) {
	if speech {
		chunk.PCM = append(chunk.PCM, f.prefix...)
		chunk.PCM = append(chunk.PCM, pcm...)
		f.prefix = nil
		f.active = true
		chunk.Ended = false
		return
	}
	if f.active {
		chunk.Ended = true
		f.active = false
	}
	f.prefix = append(f.prefix, pcm...)
	if len(f.prefix) > f.padding {
		f.prefix = f.prefix[len(f.prefix)-f.padding:]
	}
}

func (f *streamFilter) Flush(ctx context.Context) (Chunk, error) {
	defer f.reset()
	if err := ctx.Err(); err != nil {
		return Chunk{}, err
	}
	var chunk Chunk
	// A zero-padded final window and one lookahead window let Silero classify
	// the last real samples. Artificial padding is never sent to GigaAM.
	frame := make([]byte, vadWindowSamples*2)
	copy(frame, f.pending)
	f.collect(&chunk, f.pending[f.sent:], f.detector.Speech(Samples(frame)))
	f.collect(&chunk, nil, f.detector.Speech(make([]float32, vadWindowSamples)))
	chunk.Ended = true
	return chunk, nil
}

func (f *streamFilter) reset() {
	f.detector.Reset()
	f.prefix, f.pending = nil, nil
	f.sent, f.active = 0, false
}

func (f *streamFilter) Close() {
	if f.detector != nil {
		f.detector.Close()
		f.detector = nil
	}
	f.prefix, f.pending = nil, nil
}

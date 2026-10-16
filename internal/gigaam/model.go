// Package gigaam runs GigaAM locally through sherpa-onnx's native Go bindings.
package gigaam

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/config"
)

type Model struct {
	mu        sync.RWMutex
	closed    bool
	available chan *sherpa.OfflineRecognizer
	all       []*sherpa.OfflineRecognizer
	maxBytes  int
}

func New(c config.Config) (*Model, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	if c.Provider == "cuda" {
		// Keep CUDA's actual sherpa sessions in FP32 math too.
		if err := os.Setenv("NVIDIA_TF32_OVERRIDE", "0"); err != nil {
			return nil, err
		}
		if err := checkCUDA(); err != nil {
			return nil, err
		}
	}

	for _, path := range []string{c.Encoder, c.Decoder, c.Joiner, c.Tokens} {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("model file %q: %w; mount a sherpa-compatible model directory into the container", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("invalid model file %q", path)
		}
	}
	m := &Model{
		available: make(chan *sherpa.OfflineRecognizer, c.ModelWorkers),
		maxBytes:  c.MaxAudioSeconds * audio.SampleRate * 2,
	}
	for i := 0; i < c.ModelWorkers; i++ {
		rc := recognizerConfig(c)
		r := sherpa.NewOfflineRecognizer(&rc)
		if r == nil {
			m.Close()
			return nil, fmt.Errorf("sherpa-onnx failed to load GigaAM (use a sherpa-compatible export)")
		}
		m.all = append(m.all, r)
		m.available <- r
	}

	return m, nil
}

func recognizerConfig(cfg config.Config) sherpa.OfflineRecognizerConfig {
	recognizer := sherpa.OfflineRecognizerConfig{}
	recognizer.FeatConfig.SampleRate = audio.SampleRate
	recognizer.FeatConfig.FeatureDim = 64

	model := &recognizer.ModelConfig
	model.Transducer.Encoder = cfg.Encoder
	model.Transducer.Decoder = cfg.Decoder
	model.Transducer.Joiner = cfg.Joiner
	model.Tokens = cfg.Tokens
	model.NumThreads = cfg.NumThreads
	model.Provider = cfg.Provider
	model.ModelType = "nemo_transducer"

	recognizer.DecodingMethod = "greedy_search"
	return recognizer
}

func (m *Model) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if len(pcm) == 0 {
		return "", nil
	}

	if len(pcm)%2 != 0 {
		return "", audio.ErrAudio
	}

	if len(pcm) > m.maxBytes {
		return "", audio.ErrLimit
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return "", fmt.Errorf("model is closed")
	}

	var r *sherpa.OfflineRecognizer
	select {
	case r = <-m.available:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { m.available <- r }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s := sherpa.NewOfflineStream(r)
	if s == nil {
		return "", fmt.Errorf("cannot allocate recognition stream")
	}
	defer sherpa.DeleteOfflineStream(s)
	s.AcceptWaveform(audio.SampleRate, audio.Samples(pcm))
	// The native call is synchronous. Cancellation is checked before and after it.
	r.Decode(s)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result := s.GetResult()
	if result == nil {
		return "", fmt.Errorf("recognizer returned no result")
	}

	return strings.TrimSpace(result.Text), nil
}

func (m *Model) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, r := range m.all {
		sherpa.DeleteOfflineRecognizer(r)
	}
	m.all = nil
}

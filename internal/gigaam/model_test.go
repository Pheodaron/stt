package gigaam

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/config"
)

func TestCUDAUnavailabilityDoesNotFallBackToCPU(t *testing.T) {
	if err := checkCUDA(); err == nil {
		t.Skip("CUDA is available on this machine")
	}
	t.Setenv("MODEL_PROVIDER", "cuda")
	t.Setenv("MODEL_PRECISION", "fp32")
	t.Setenv("NVIDIA_TF32_OVERRIDE", "0")
	c, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(c)
	if err == nil || !strings.Contains(err.Error(), "CUDA requested") {
		t.Fatalf("expected CUDA readiness failure before model load: %v", err)
	}
}

// Opt in with MODEL_TEST_AUDIO and MODEL_DIR. This uses actual ONNX weights.
func TestRealGigaAM(t *testing.T) {
	path := os.Getenv("MODEL_TEST_AUDIO")
	if path == "" {
		t.Skip("set MODEL_TEST_AUDIO and MODEL_DIR to test actual inference")
	}
	c, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := audio.Processor{FFmpeg: c.FFmpeg, MaxBytes: 4 << 20, MaxSeconds: 25}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pcm, err := p.Decode(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, err := m.Transcribe(ctx, pcm)
			if err != nil {
				t.Error(err)
				return
			}
			expect := os.Getenv("MODEL_TEST_EXPECT")
			if len([]rune(text)) < 2 || (expect != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(expect))) {
				t.Errorf("unexpected transcription: %q", text)
			}
			t.Log(text)
		}()
	}
	wg.Wait()
	m.Close()
	m.Close()
	if _, err := m.Transcribe(ctx, pcm); err == nil {
		t.Fatal("closed model accepted audio")
	}
}

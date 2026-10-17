package config

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestEmptyAndUnsetSettingsUseDefaults(t *testing.T) {
	keys := []string{
		"URL", "PORT", "CLIENT_TYPE", "PING_INTERVAL", "LOG_LEVEL",
		"MAX_SEGMENT_BYTES", "MAX_AUDIO_SECONDS", "REQUEST_TIMEOUT",
		"BUFFER_SIZE", "BUFFER_OVERLAP_MS",
		"VAD_MODEL_PATH", "VAD_THRESHOLD", "PADDING_DURATION_MS", "VAD_MIN_SILENCE_MS",
		"MODEL_PROVIDER", "NUM_THREADS", "MODEL_WORKERS",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	empty, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	unset, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Host: "0.0.0.0", Port: "8080", ClientType: "grpc", Level: slog.LevelInfo,
		PingInterval:    30 * time.Second,
		MaxSegmentBytes: 4 << 20, MaxAudioSeconds: 25, RequestTimeout: time.Minute,
		BufferSize: 10, OverlapMS: 500,
		VADModel: "/app/models/silero-v6.2.3/silero_vad.onnx", VADThreshold: 0.35,
		PaddingMS: 250, VADSilenceMS: 500,
		Provider: "cpu", NumThreads: 2, ModelWorkers: 1,
	}
	if empty != want || unset != want {
		t.Fatalf("empty: %+v\nunset: %+v\nwant: %+v", empty, unset, want)
	}
}

func TestFromEnvReadsConfiguredValues(t *testing.T) {
	settings := map[string]string{
		"URL": "::1", "PORT": "9000", "CLIENT_TYPE": "websocket",
		"PING_INTERVAL": "15", "LOG_LEVEL": "debug",
		"MAX_SEGMENT_BYTES": "1048576", "MAX_AUDIO_SECONDS": "20", "REQUEST_TIMEOUT": "45",
		"BUFFER_SIZE": "4", "BUFFER_OVERLAP_MS": "200",
		"VAD_MODEL_PATH": "/tmp/silero.onnx", "VAD_THRESHOLD": "0.25",
		"PADDING_DURATION_MS": "100", "VAD_MIN_SILENCE_MS": "300",
		"MODEL_PROVIDER": "cuda", "NUM_THREADS": "3", "MODEL_WORKERS": "2",
	}
	for key, value := range settings {
		t.Setenv(key, value)
	}
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Host: "::1", Port: "9000", ClientType: "websocket", Level: slog.LevelDebug,
		PingInterval:    15 * time.Second,
		MaxSegmentBytes: 1 << 20, MaxAudioSeconds: 20, RequestTimeout: 45 * time.Second,
		BufferSize: 4, OverlapMS: 200,
		VADModel: "/tmp/silero.onnx", VADThreshold: 0.25, PaddingMS: 100, VADSilenceMS: 300,
		Provider: "cuda", NumThreads: 3, ModelWorkers: 2,
	}
	if c != want || c.Address() != "[::1]:9000" {
		t.Fatalf("got: %+v\nwant: %+v", c, want)
	}
}

func TestProviderSelection(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "cpu")
	if err := os.Unsetenv("MODEL_PROVIDER"); err != nil {
		t.Fatal(err)
	}
	c, err := FromEnv()
	if err != nil || c.Provider != "cpu" {
		t.Fatalf("default provider: %+v %v", c, err)
	}
	t.Setenv("MODEL_PROVIDER", "cuda")
	c, err = FromEnv()
	if err != nil || c.Provider != "cuda" {
		t.Fatalf("CUDA: %+v %v", c, err)
	}
	t.Setenv("MODEL_PROVIDER", "auto")
	if _, err := FromEnv(); err == nil {
		t.Fatal("implicit provider fallback accepted")
	}
}

func TestValidation(t *testing.T) {
	keys := []string{
		"BUFFER_SIZE",
		"VAD_THRESHOLD",
		"VAD_MIN_SILENCE_MS",
		"BUFFER_OVERLAP_MS",
		"PADDING_DURATION_MS",
		"NUM_THREADS",
		"MODEL_WORKERS",
		"MAX_AUDIO_SECONDS",
		"CLIENT_TYPE",
		"PORT",
		"PING_INTERVAL",
		"REQUEST_TIMEOUT",
		"LOG_LEVEL",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "invalid")
			if _, err := FromEnv(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Setenv("VAD_THRESHOLD", "0.35")
	t.Setenv("PADDING_DURATION_MS", "200")
	t.Setenv("BUFFER_SIZE", "1")
	t.Setenv("CLIENT_TYPE", "websocket")
	c, err := FromEnv()
	if err != nil || c.VADThreshold != 0.35 || c.BufferSize != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("PADDING_DURATION_MS", "501")
	if _, err := FromEnv(); err == nil {
		t.Fatal("padding longer than endpoint silence accepted")
	}
}

func TestVADThresholdRejectsNonFiniteAndOutOfRangeValues(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf", "0", "1", "0.001"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("VAD_THRESHOLD", value)
			if _, err := FromEnv(); err == nil {
				t.Fatal("invalid threshold accepted")
			}
		})
	}
}

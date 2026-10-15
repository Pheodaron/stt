package config

import (
	"os"
	"testing"
)

func TestFP32IsDefaultAndINT8IsExplicit(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "cpu")
	t.Setenv("ENCODER_PATH", "")
	if err := os.Unsetenv("ENCODER_PATH"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODEL_DIR", "models/test")
	t.Setenv("MODEL_PRECISION", "fp32")
	if err := os.Unsetenv("MODEL_PRECISION"); err != nil {
		t.Fatal(err)
	}
	c, err := FromEnv()
	if err != nil || c.Encoder != "models/test/encoder.onnx" {
		t.Fatalf("FP32: %+v %v", c, err)
	}
	t.Setenv("MODEL_PRECISION", "int8")
	c, err = FromEnv()
	if err != nil || c.Encoder != "models/test/encoder.int8.onnx" {
		t.Fatalf("INT8: %+v %v", c, err)
	}
	t.Setenv("MODEL_PRECISION", "fp16")
	if _, err := FromEnv(); err == nil {
		t.Fatal("unsupported precision accepted")
	}
}

func TestProviderSelection(t *testing.T) {
	t.Setenv("MODEL_PROVIDER", "cpu")
	if err := os.Unsetenv("MODEL_PROVIDER"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODEL_PRECISION", "fp32")
	c, err := FromEnv()
	if err != nil || c.Provider != "cpu" {
		t.Fatalf("default provider: %+v %v", c, err)
	}
	t.Setenv("MODEL_PROVIDER", "cuda")
	c, err = FromEnv()
	if err != nil || c.Provider != "cuda" {
		t.Fatalf("CUDA: %+v %v", c, err)
	}
	t.Setenv("MODEL_PRECISION", "int8")
	if _, err := FromEnv(); err == nil {
		t.Fatal("CUDA accepted the CPU INT8 profile")
	}
	t.Setenv("MODEL_PRECISION", "fp32")
	t.Setenv("MODEL_PROVIDER", "auto")
	if _, err := FromEnv(); err == nil {
		t.Fatal("implicit provider fallback accepted")
	}
}

func TestValidation(t *testing.T) {
	keys := []string{
		"BUFFER_SIZE",
		"FRAME_DURATION_MS",
		"PADDING_DURATION_MS",
		"NUM_THREADS",
		"MODEL_WORKERS",
		"MAX_AUDIO_SECONDS",
		"CLIENT_TYPE",
		"PORT",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "invalid")
			if _, err := FromEnv(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Setenv("FRAME_DURATION_MS", "20")
	t.Setenv("PADDING_DURATION_MS", "200")
	t.Setenv("BUFFER_SIZE", "1")
	t.Setenv("CLIENT_TYPE", "websocket")
	c, err := FromEnv()
	if err != nil || c.FrameMS != 20 || c.BufferSize != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("PADDING_DURATION_MS", "201")
	if _, err := FromEnv(); err == nil {
		t.Fatal("unaligned padding accepted")
	}
}

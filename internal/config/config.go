package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config keeps the original environment names and adds inference settings.
type Config struct {
	Host       string
	Port       string
	ClientType string
	Level      slog.Level

	FFmpeg          string
	BufferSize      int
	FrameMS         int
	PaddingMS       int
	VADMode         int
	MaxSegmentBytes int
	MaxAudioSeconds int

	PingInterval   time.Duration
	RequestTimeout time.Duration

	Provider     string
	Precision    string
	NumThreads   int
	ModelWorkers int
	Encoder      string
	Decoder      string
	Joiner       string
	Tokens       string
}

func FromEnv() (Config, error) {
	c := Config{
		Host:       value("URL", "0.0.0.0"),
		Port:       value("PORT", "8080"),
		ClientType: value("CLIENT_TYPE", "grpc"),
		FFmpeg:     value("FFMPEG_PATH", "ffmpeg"),
		Provider:   value("MODEL_PROVIDER", "cpu"),
		Precision:  value("MODEL_PRECISION", "fp32"),
	}

	if err := c.readIntegers(); err != nil {
		return c, err
	}

	if err := c.readDurations(); err != nil {
		return c, err
	}

	if err := c.readLogLevel(); err != nil {
		return c, err
	}

	c.readModelPaths()
	return c, c.Validate()
}

func (c *Config) readIntegers() error {
	settings := []struct {
		key      string
		target   *int
		fallback int
	}{
		{"BUFFER_SIZE", &c.BufferSize, 10},
		{"FRAME_DURATION_MS", &c.FrameMS, 10},
		{"PADDING_DURATION_MS", &c.PaddingMS, 200},
		{"VAD_MODE", &c.VADMode, 3},
		{"MAX_SEGMENT_BYTES", &c.MaxSegmentBytes, 4 << 20},
		{"MAX_AUDIO_SECONDS", &c.MaxAudioSeconds, 25},
		{"NUM_THREADS", &c.NumThreads, 2},
		{"MODEL_WORKERS", &c.ModelWorkers, 1},
	}

	for _, setting := range settings {
		raw := value(setting.key, strconv.Itoa(setting.fallback))
		number, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%s must be an integer: %w", setting.key, err)
		}

		*setting.target = number
	}

	return nil
}

func (c *Config) readDurations() error {
	settings := []struct {
		key      string
		target   *time.Duration
		fallback string
	}{
		{"PING_INTERVAL", &c.PingInterval, "30"},
		{"REQUEST_TIMEOUT", &c.RequestTimeout, "60"},
	}

	for _, setting := range settings {
		seconds, err := strconv.Atoi(value(setting.key, setting.fallback))
		if err != nil || seconds < 1 || seconds > 3600 {
			return fmt.Errorf("%s must be 1..3600 seconds", setting.key)
		}

		*setting.target = time.Duration(seconds) * time.Second
	}

	return nil
}

func (c *Config) readModelPaths() {
	dir := value("MODEL_DIR", "models/gigaam-v3-"+c.Precision)
	encoderFile := "encoder.onnx"
	if c.Precision == "int8" {
		encoderFile = "encoder.int8.onnx"
	}

	c.Encoder = value("ENCODER_PATH", filepath.Join(dir, encoderFile))
	c.Decoder = value("DECODER_PATH", filepath.Join(dir, "decoder.onnx"))
	c.Joiner = value("JOINER_PATH", filepath.Join(dir, "joiner.onnx"))
	c.Tokens = value("TOKENS_PATH", filepath.Join(dir, "tokens.txt"))
}

func (c *Config) readLogLevel() error {
	switch value("LOG_LEVEL", "info") {
	case "debug":
		c.Level = slog.LevelDebug
	case "info":
		c.Level = slog.LevelInfo
	case "warn":
		c.Level = slog.LevelWarn
	case "error", "critical":
		c.Level = slog.LevelError
	default:
		return fmt.Errorf("invalid LOG_LEVEL")
	}

	return nil
}

func (c Config) Address() string {
	return net.JoinHostPort(c.Host, c.Port)
}

func (c Config) Validate() error {
	for _, validate := range []func() error{c.validateInference, c.validateServer, c.validateAudio} {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

func (c Config) validateInference() error {
	if c.Provider != "cpu" && c.Provider != "cuda" {
		return fmt.Errorf("MODEL_PROVIDER must be cpu or cuda")
	}

	if c.Precision != "fp32" && c.Precision != "int8" {
		return fmt.Errorf("MODEL_PRECISION must be fp32 or int8; fp32 preserves unquantized weights")
	}

	if c.Provider == "cuda" && c.Precision != "fp32" {
		return fmt.Errorf("CUDA mode requires MODEL_PRECISION=fp32; INT8 remains a CPU-only option")
	}

	if c.NumThreads < 1 || c.NumThreads > 64 || c.ModelWorkers < 1 || c.ModelWorkers > 16 {
		return fmt.Errorf("NUM_THREADS must be 1..64 and MODEL_WORKERS 1..16")
	}

	return nil
}

func (c Config) validateServer() error {
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("PORT must be 1..65535")
	}

	if c.ClientType != "grpc" && c.ClientType != "websocket" {
		return fmt.Errorf("CLIENT_TYPE must be grpc or websocket")
	}

	return nil
}

func (c Config) validateAudio() error {
	if c.BufferSize < 1 || c.BufferSize > 64 {
		return fmt.Errorf("BUFFER_SIZE must be 1..64")
	}

	if c.FrameMS != 10 && c.FrameMS != 20 && c.FrameMS != 30 {
		return fmt.Errorf("FRAME_DURATION_MS must be 10, 20 or 30")
	}

	if c.PaddingMS < c.FrameMS || c.PaddingMS > 2000 || c.PaddingMS%c.FrameMS != 0 {
		return fmt.Errorf("PADDING_DURATION_MS must be a multiple of FRAME_DURATION_MS, up to 2000")
	}

	if c.VADMode < 0 || c.VADMode > 3 {
		return fmt.Errorf("VAD_MODE must be 0..3")
	}

	if c.MaxSegmentBytes < 1 || c.MaxSegmentBytes > 64<<20 {
		return fmt.Errorf("MAX_SEGMENT_BYTES must be 1..67108864")
	}

	if c.MaxAudioSeconds < 1 || c.MaxAudioSeconds > 25 {
		return fmt.Errorf("MAX_AUDIO_SECONDS must be 1..25")
	}

	return nil
}

func value(key, fallback string) string {
	if s, ok := os.LookupEnv(key); ok {
		return s
	}

	return fallback
}

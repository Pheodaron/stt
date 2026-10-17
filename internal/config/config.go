package config

import (
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"strconv"
	"time"
)

// These assets are installed at fixed paths by both container images.
const (
	FFmpegPath = "/usr/local/bin/ffmpeg"
	ModelDir   = "/app/models/gigaam-v3-fp32"
)

type Config struct {
	// Server and logging.
	Host         string
	Port         string
	ClientType   string
	Level        slog.Level
	PingInterval time.Duration

	// Request limits.
	MaxSegmentBytes int
	MaxAudioSeconds int
	RequestTimeout  time.Duration

	// Recognition windows.
	BufferSize int
	OverlapMS  int

	// Voice activity detection.
	VADModel     string
	VADThreshold float32
	PaddingMS    int
	VADSilenceMS int

	// GigaAM inference.
	Provider     string
	NumThreads   int
	ModelWorkers int
}

func FromEnv() (Config, error) {
	var c Config
	var err error

	// Server and logging.
	c.Host = value("URL", "0.0.0.0")
	c.Port = value("PORT", "8080")
	c.ClientType = value("CLIENT_TYPE", "grpc")
	c.PingInterval, err = seconds("PING_INTERVAL", 30)
	if err != nil {
		return c, err
	}
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
		return c, fmt.Errorf("invalid LOG_LEVEL")
	}

	// Request limits.
	c.MaxSegmentBytes, err = integer("MAX_SEGMENT_BYTES", 4<<20)
	if err != nil {
		return c, err
	}
	c.MaxAudioSeconds, err = integer("MAX_AUDIO_SECONDS", 25)
	if err != nil {
		return c, err
	}
	c.RequestTimeout, err = seconds("REQUEST_TIMEOUT", 60)
	if err != nil {
		return c, err
	}

	// Recognition windows.
	c.BufferSize, err = integer("BUFFER_SIZE", 10)
	if err != nil {
		return c, err
	}
	c.OverlapMS, err = integer("BUFFER_OVERLAP_MS", 500)
	if err != nil {
		return c, err
	}

	// Voice activity detection.
	c.VADModel = value("VAD_MODEL_PATH", "/app/models/silero-v6.2.3/silero_vad.onnx")
	threshold, err := strconv.ParseFloat(value("VAD_THRESHOLD", "0.35"), 32)
	if err != nil {
		return c, fmt.Errorf("VAD_THRESHOLD must be a finite number in [0.01, 1)")
	}
	c.VADThreshold = float32(threshold)
	c.PaddingMS, err = integer("PADDING_DURATION_MS", 250)
	if err != nil {
		return c, err
	}
	c.VADSilenceMS, err = integer("VAD_MIN_SILENCE_MS", 500)
	if err != nil {
		return c, err
	}

	// GigaAM inference; the container always contains the same FP32 model.
	c.Provider = value("MODEL_PROVIDER", "cpu")
	c.NumThreads, err = integer("NUM_THREADS", 2)
	if err != nil {
		return c, err
	}
	c.ModelWorkers, err = integer("MODEL_WORKERS", 1)
	if err != nil {
		return c, err
	}

	return c, c.Validate()
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

	if c.PaddingMS < 64 || c.PaddingMS > 2000 {
		return fmt.Errorf("PADDING_DURATION_MS must be 64..2000")
	}
	if c.VADSilenceMS < c.PaddingMS || c.VADSilenceMS > 3000 {
		return fmt.Errorf("VAD_MIN_SILENCE_MS must be at least PADDING_DURATION_MS and at most 3000")
	}
	if math.IsNaN(float64(c.VADThreshold)) || c.VADThreshold < 0.01 || c.VADThreshold >= 1 || c.VADModel == "" {
		return fmt.Errorf("VAD_MODEL_PATH and a VAD_THRESHOLD in [0.01, 1) are required")
	}
	if c.OverlapMS < 0 || c.OverlapMS > 2000 || c.OverlapMS >= c.MaxAudioSeconds*1000 {
		return fmt.Errorf("BUFFER_OVERLAP_MS must be 0..2000 and less than MAX_AUDIO_SECONDS")
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
	if s := os.Getenv(key); s != "" {
		return s
	}

	return fallback
}

func integer(key string, fallback int) (int, error) {
	number, err := strconv.Atoi(value(key, strconv.Itoa(fallback)))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return number, nil
}

func seconds(key string, fallback int) (time.Duration, error) {
	number, err := integer(key, fallback)
	if err != nil || number < 1 || number > 3600 {
		return 0, fmt.Errorf("%s must be 1..3600 seconds", key)
	}
	return time.Duration(number) * time.Second, nil
}

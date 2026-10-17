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

// FromEnv panics on invalid configuration, before startup acquires resources.
func FromEnv() Config {
	var c Config

	// Server and logging.
	c.Host = value("URL", "0.0.0.0")
	c.Port = value("PORT", "8080")
	c.ClientType = value("CLIENT_TYPE", "grpc")
	c.PingInterval = seconds("PING_INTERVAL", 30)
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
		panic("LOG_LEVEL must be debug, info, warn, error or critical")
	}

	// Request limits.
	c.MaxSegmentBytes = integer("MAX_SEGMENT_BYTES", 4<<20)
	c.MaxAudioSeconds = integer("MAX_AUDIO_SECONDS", 25)
	c.RequestTimeout = seconds("REQUEST_TIMEOUT", 60)

	// Recognition windows.
	c.BufferSize = integer("BUFFER_SIZE", 10)
	c.OverlapMS = integer("BUFFER_OVERLAP_MS", 500)

	// Voice activity detection.
	c.VADModel = value("VAD_MODEL_PATH", "/app/models/silero-v6.2.3/silero_vad.onnx")
	threshold, err := strconv.ParseFloat(value("VAD_THRESHOLD", "0.35"), 32)
	if err != nil {
		panic("VAD_THRESHOLD must be a finite number in [0.01, 1)")
	}
	c.VADThreshold = float32(threshold)
	c.PaddingMS = integer("PADDING_DURATION_MS", 250)
	c.VADSilenceMS = integer("VAD_MIN_SILENCE_MS", 500)

	// GigaAM inference; the container always contains the same FP32 model.
	c.Provider = value("MODEL_PROVIDER", "cpu")
	c.NumThreads = integer("NUM_THREADS", 2)
	c.ModelWorkers = integer("MODEL_WORKERS", 1)

	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
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

	if c.NumThreads < 1 || c.NumThreads > 64 {
		return fmt.Errorf("NUM_THREADS must be 1..64")
	}
	if c.ModelWorkers < 1 || c.ModelWorkers > 16 {
		return fmt.Errorf("MODEL_WORKERS must be 1..16")
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
	if c.MaxSegmentBytes < 1 || c.MaxSegmentBytes > 64<<20 {
		return fmt.Errorf("MAX_SEGMENT_BYTES must be 1..67108864")
	}
	if c.MaxAudioSeconds < 1 || c.MaxAudioSeconds > 25 {
		return fmt.Errorf("MAX_AUDIO_SECONDS must be 1..25")
	}

	if c.BufferSize < 1 || c.BufferSize > 64 {
		return fmt.Errorf("BUFFER_SIZE must be 1..64")
	}

	if c.PaddingMS < 64 || c.PaddingMS > 2000 {
		return fmt.Errorf("PADDING_DURATION_MS must be 64..2000")
	}
	if c.VADSilenceMS < c.PaddingMS || c.VADSilenceMS > 3000 {
		return fmt.Errorf("VAD_MIN_SILENCE_MS must be at least PADDING_DURATION_MS and at most 3000")
	}
	if c.VADModel == "" {
		return fmt.Errorf("VAD_MODEL_PATH is required")
	}
	if math.IsNaN(float64(c.VADThreshold)) || c.VADThreshold < 0.01 || c.VADThreshold >= 1 {
		return fmt.Errorf("VAD_THRESHOLD must be a finite number in [0.01, 1)")
	}
	if c.OverlapMS < 0 || c.OverlapMS > 2000 || c.OverlapMS >= c.MaxAudioSeconds*1000 {
		return fmt.Errorf("BUFFER_OVERLAP_MS must be 0..2000 and less than MAX_AUDIO_SECONDS")
	}

	return nil
}

func value(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}

	return fallback
}

func integer(key string, fallback int) int {
	number, err := strconv.Atoi(value(key, strconv.Itoa(fallback)))
	if err != nil {
		panic(fmt.Sprintf("%s must be an integer", key))
	}
	return number
}

func seconds(key string, fallback int) time.Duration {
	number := integer(key, fallback)
	if number < 1 || number > 3600 {
		panic(fmt.Sprintf("%s must be 1..3600 seconds", key))
	}
	return time.Duration(number) * time.Second
}

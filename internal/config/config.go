package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// These assets are installed at fixed paths by both container images.
const (
	FFmpegPath = "/usr/local/bin/ffmpeg"
	ModelDir   = "/app/models/gigaam-v3-fp32"
	VadPath    = "/app/models/silero-v6.2.3/silero_vad.onnx"
)

type Config struct {
	// Server and logging.
	Host         string
	Port         string
	ClientType   string
	PingInterval time.Duration
	IsDev        bool

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

func FromEnv() Config {
	c := Config{
		Host:            value("URL", "0.0.0.0"),
		Port:            value("PORT", "8080"),
		ClientType:      value("CLIENT_TYPE", "grpc"),
		PingInterval:    seconds("PING_INTERVAL", 30),
		MaxSegmentBytes: integer("MAX_SEGMENT_BYTES", 4<<20),
		MaxAudioSeconds: integer("MAX_AUDIO_SECONDS", 25),
		RequestTimeout:  seconds("REQUEST_TIMEOUT", 60),
		BufferSize:      integer("BUFFER_SIZE", 10),
		OverlapMS:       integer("BUFFER_OVERLAP_MS", 500),
		PaddingMS:       integer("PADDING_DURATION_MS", 250),
		VADSilenceMS:    integer("VAD_MIN_SILENCE_MS", 500),
		Provider:        value("MODEL_PROVIDER", "cpu"),
		NumThreads:      integer("NUM_THREADS", 2),
		ModelWorkers:    integer("MODEL_WORKERS", 1),
		VADThreshold:    vadThreshold("0.35"),
		IsDev:           strings.ToLower(os.Getenv("IS_DEV")) == "dev",
	}

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

func vadThreshold(fallback string) float32 {
	threshold, err := strconv.ParseFloat(value("VAD_THRESHOLD", fallback), 32)
	if err != nil {
		panic("VAD_THRESHOLD must be a finite number in [0.01, 1)")
	}
	return float32(threshold)
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/config"
	"vks-stt-v2/internal/gigaam"
	"vks-stt-v2/internal/stt"
)

const shutdownTimeout = 10 * time.Second

func main() {
	envFile := flag.String("env", ".env", "Optional dotenv configuration")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, *envFile); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, envFile string) error {
	cfg, err := config.Load(envFile)
	if err != nil {
		return err
	}

	logOptions := &slog.HandlerOptions{Level: cfg.Level}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, logOptions)))

	if _, err := exec.LookPath(cfg.FFmpeg); err != nil {
		return fmt.Errorf("FFmpeg is required: %w", err)
	}

	model, err := gigaam.New(cfg)
	if err != nil {
		return err
	}
	defer model.Close()

	listener, err := net.Listen("tcp", cfg.Address())
	if err != nil {
		return err
	}
	defer listener.Close()

	service := newService(cfg, model)
	logReady(cfg, listener.Addr())

	if cfg.ClientType == "websocket" {
		return serveWebSocket(ctx, listener, service, cfg)
	}

	return serveGRPC(ctx, listener, service, cfg)
}

func newService(cfg config.Config, model stt.Model) *stt.Service {
	processor := audio.Processor{
		FFmpeg:     cfg.FFmpeg,
		FrameMS:    cfg.FrameMS,
		PaddingMS:  cfg.PaddingMS,
		VADMode:    cfg.VADMode,
		MaxBytes:   cfg.MaxSegmentBytes,
		MaxSeconds: cfg.MaxAudioSeconds,
	}

	return &stt.Service{
		Processor:  processor,
		Model:      model,
		BufferSize: cfg.BufferSize,
		FrameMS:    cfg.FrameMS,
		MaxSeconds: cfg.MaxAudioSeconds,
		Timeout:    cfg.RequestTimeout,
		Slots:      make(chan struct{}, cfg.ModelWorkers*2),
	}
}

func logReady(cfg config.Config, address net.Addr) {
	slog.Info("ready",
		"address", address,
		"protocol", cfg.ClientType,
		"engine", "sherpa-onnx",
		"provider", cfg.Provider,
		"precision", cfg.Precision,
		"threads", cfg.NumThreads,
		"workers", cfg.ModelWorkers,
	)
}

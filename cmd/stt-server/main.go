package main

import (
	"context"
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
	"vks-stt-v2/internal/diarization"
	"vks-stt-v2/internal/gigaam"
	"vks-stt-v2/internal/inference"
	"vks-stt-v2/internal/logger"
	"vks-stt-v2/internal/meeting"
	"vks-stt-v2/internal/stt"
)

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg := config.FromEnv()
	logger.Init(cfg)

	if _, err := exec.LookPath(config.FFmpegPath); err != nil {
		return fmt.Errorf("FFmpeg is required: %w", err)
	}

	model, err := gigaam.New(cfg)
	if err != nil {
		return err
	}
	defer model.Close()
	vad, err := audio.NewSilero(audio.VADConfig{
		Model:        cfg.VADModel,
		Threshold:    cfg.VADThreshold,
		PaddingMS:    cfg.PaddingMS,
		MinSilenceMS: cfg.VADSilenceMS,
	})
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.Address())
	if err != nil {
		return err
	}
	defer listener.Close()

	priority := inference.NewPriority(model, cfg.ModelWorkers)
	service := newService(cfg, priority, vad)
	var meetings *meeting.Service
	if cfg.ClientType == "grpc" {
		diarizer, err := diarization.New(config.DiarizationLibrary, config.DiarizationModel)
		if err != nil {
			return err
		}
		defer diarizer.Close()
		meetings = &meeting.Service{
			FFmpeg: config.FFmpegPath, TempDir: cfg.MeetingTempDir,
			ChunkBytes: cfg.MeetingChunkBytes, MaxSeconds: cfg.MaxAudioSeconds,
			Diarizer: diarizer, Model: priority.Batch(), FindPause: vad.FindPause,
		}
		if err := meetings.Init(); err != nil {
			return err
		}
		defer meetings.Close()
	}
	logReady(cfg, listener.Addr())

	if cfg.ClientType == "websocket" {
		return serveWebSocket(ctx, listener, service, cfg)
	}

	return serveGRPC(ctx, listener, service, cfg, meetings)
}

func newService(cfg config.Config, model stt.Model, vad stt.VAD) *stt.Service {
	processor := audio.Processor{
		FFmpeg:     config.FFmpegPath,
		MaxBytes:   cfg.MaxSegmentBytes,
		MaxSeconds: cfg.MaxAudioSeconds,
	}

	return &stt.Service{
		Decoder:    processor,
		VAD:        vad,
		Model:      model,
		BufferSize: cfg.BufferSize,
		OverlapMS:  cfg.OverlapMS,
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
		"precision", "fp32",
		"threads", cfg.NumThreads,
		"workers", cfg.ModelWorkers,
		"vad", "silero-v6.2.3",
		"vad_provider", "cpu",
		"vad_threshold", cfg.VADThreshold,
	)
}

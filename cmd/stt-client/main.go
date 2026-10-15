package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"
)

type clientOptions struct {
	Protocol       string
	Address        string
	File           string
	SegmentSeconds float64
	Unbuffered     bool
	Timeout        time.Duration
}

func main() {
	options := readFlags()
	if options.File == "" || options.SegmentSeconds <= 0 || options.SegmentSeconds > 25 {
		log.Fatal("provide -file and -segment-seconds in (0,25]")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	if err := run(ctx, options); err != nil {
		log.Fatal(err)
	}
}

func readFlags() clientOptions {
	var options clientOptions
	flag.StringVar(&options.Protocol, "protocol", "grpc", "grpc or websocket")
	flag.StringVar(&options.Address, "address", "localhost:8080", "gRPC host:port or ws:// URL")
	flag.StringVar(&options.File, "file", "", "Audio file")
	flag.Float64Var(&options.SegmentSeconds, "segment-seconds", 1, "Segment duration")
	flag.BoolVar(&options.Unbuffered, "unbuffered", false, "Recognize each segment independently")
	flag.DurationVar(&options.Timeout, "timeout", 10*time.Minute, "Timeout for the whole file")
	flag.Parse()

	return options
}

func run(ctx context.Context, options clientOptions) error {
	if options.Protocol != "grpc" && options.Protocol != "websocket" {
		return fmt.Errorf("protocol must be grpc or websocket")
	}

	directory, err := os.MkdirTemp("", "vks-stt-client-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)

	files, err := splitAudio(ctx, options.File, directory, options.SegmentSeconds)
	if err != nil {
		return err
	}

	if options.Protocol == "websocket" {
		return sendWebSocket(ctx, options.Address, files, options.Unbuffered)
	}

	return sendGRPC(ctx, options.Address, files, options.Unbuffered)
}

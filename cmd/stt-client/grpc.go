package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	sttv1 "vks-stt-v2/gen/stt/v1"
)

func sendGRPC(ctx context.Context, address string, files []string, unbuffered bool) error {
	credentials := grpc.WithTransportCredentials(insecure.NewCredentials())
	connection, err := grpc.NewClient(address, credentials)
	if err != nil {
		return err
	}
	defer connection.Close()

	client := sttv1.NewGigaamClient(connection)
	if unbuffered {
		return sendUnary(ctx, client, files)
	}

	return sendStreaming(ctx, client, files)
}

func sendUnary(ctx context.Context, client sttv1.GigaamClient, files []string) error {
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		result, err := client.TranscribeUnbuffered(ctx, &sttv1.VoiceChunk{Segment: data})
		if err != nil {
			return err
		}
		printResult(result)
	}

	return nil
}

func sendStreaming(ctx context.Context, client sttv1.GigaamClient, files []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.Transcribe(ctx)
	if err != nil {
		return err
	}

	// Send and receive concurrently so a large file cannot deadlock the stream.
	sent := make(chan error, 1)
	go func() {
		sent <- sendSegments(stream, files, cancel)
	}()

	for {
		result, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return <-sent
		}
		if err != nil {
			return err
		}
		printResult(result)
	}
}

func sendSegments(stream sttv1.Gigaam_TranscribeClient, files []string, cancel context.CancelFunc) error {
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			cancel()
			return err
		}

		if err := stream.Send(&sttv1.VoiceChunk{Segment: data}); err != nil {
			return err
		}
	}

	return stream.CloseSend()
}

func printResult(result *sttv1.TranscribedText) {
	if result.Partial != nil {
		fmt.Printf("partial: %s\n", result.GetPartial())
	}

	if result.Message != nil {
		fmt.Printf("message: %s\n", result.GetMessage())
	}
}

// stt-client uploads a recording, saves the response and prints it as JSON.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"

	sttv1 "vks-stt-v2/gen/stt/v1"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "stt-client:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("stt-client", flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("addr", "localhost:8080", "адрес gRPC-сервера (без TLS)")
	path := flags.String("file", "", "путь к аудиофайлу встречи")
	output := flags.String("out", "", "файл результата; по умолчанию <аудиофайл>.transcription.json")
	chunkBytes := flags.Int("chunk-bytes", 1<<20, "размер чанка; не больше MEETING_CHUNK_BYTES сервера")
	timeout := flags.Duration("timeout", 0, "deadline на загрузку и обработку, например 6h; 0 — без deadline")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Использование: stt-client -file meeting.ogg [-addr localhost:8080] [-out result.json] [-timeout 6h]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return fmt.Errorf("укажите аудиофайл через -file; дополнительные аргументы не поддерживаются")
	}
	if *address == "" || *chunkBytes < 1 || *chunkBytes > 4<<20 || *timeout < 0 {
		return fmt.Errorf("-addr не может быть пустым, -chunk-bytes должен быть 1..4194304, -timeout не может быть отрицательным")
	}
	file, err := os.Open(*path)
	if err != nil {
		return fmt.Errorf("открыть аудиофайл: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("нужен непустой обычный аудиофайл")
	}
	if *output == "" {
		*output = *path + ".transcription.json"
	}
	if existing, err := os.Stat(*output); err == nil {
		if os.SameFile(info, existing) || !existing.Mode().IsRegular() {
			return fmt.Errorf("-out должен указывать на файл результата, отличный от исходного аудио")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("проверить путь результата: %w", err)
	}
	// Check that the directory is writable before starting expensive inference.
	// Rename only a complete response, preserving any previous result on errors.
	resultFile, err := os.CreateTemp(filepath.Dir(*output), ".stt-transcription-*")
	if err != nil {
		return fmt.Errorf("создать файл результата: %w", err)
	}
	defer os.Remove(resultFile.Name())
	defer resultFile.Close()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	// Cancel the RPC on local read/write failures as well as on Ctrl+C.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	response, err := transcribeMeeting(ctx, sttv1.NewGigaamClient(conn), file, uint64(info.Size()), *chunkBytes)
	if err != nil {
		return err
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}).Marshal(response)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := resultFile.Write(data); err != nil {
		return fmt.Errorf("записать результат: %w", err)
	}
	if err := resultFile.Sync(); err != nil {
		return fmt.Errorf("сохранить результат: %w", err)
	}
	if err := resultFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(resultFile.Name(), *output); err != nil {
		return fmt.Errorf("сохранить результат в %q: %w", *output, err)
	}
	_, err = stdout.Write(data)
	return err
}

func transcribeMeeting(ctx context.Context, client sttv1.GigaamClient, file io.Reader, size uint64, chunkBytes int) (*sttv1.MeetingResponse, error) {
	stream, err := client.TranscribeMeeting(ctx,
		grpc.MaxCallSendMsgSize(chunkBytes+1024), grpc.MaxCallRecvMsgSize(math.MaxInt32))
	if err != nil {
		return nil, err
	}
	// A rejected stream may report io.EOF on Send. Receive its actual gRPC status.
	send := func(request *sttv1.MeetingRequest) error {
		if err := stream.Send(request); err != nil {
			if errors.Is(err, io.EOF) {
				_, err = stream.CloseAndRecv()
				if err == nil {
					return io.ErrUnexpectedEOF
				}
			}
			return err
		}
		return nil
	}
	if err := send(&sttv1.MeetingRequest{Payload: &sttv1.MeetingRequest_Header{
		Header: &sttv1.MeetingUploadHeader{SizeBytes: size},
	}}); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Never mutate a message's byte slice after Send; gRPC may still trace it.
		buf := make([]byte, chunkBytes)
		n, readErr := io.ReadFull(file, buf)
		if n != 0 {
			if err := send(&sttv1.MeetingRequest{Payload: &sttv1.MeetingRequest_Chunk{Chunk: buf[:n]}}); err != nil {
				return nil, err
			}
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("прочитать аудиофайл: %w", readErr)
		}
	}
	return stream.CloseAndRecv()
}

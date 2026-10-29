package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	sttv1 "vks-stt-v2/gen/stt/v1"
)

type testServer struct {
	sttv1.UnimplementedGigaamServer
	handle func(grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error
}

func (s testServer) TranscribeMeeting(stream grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error {
	return s.handle(stream)
}

func serveTest(t *testing.T, handle func(grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize((4<<20)+1024), grpc.MaxSendMsgSize(8<<20), grpc.WaitForHandlers(true))
	sttv1.RegisterGigaamServer(server, testServer{handle: handle})
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func writeRecording(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meeting.ogg")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIUploadsChunksAndPrintsCompleteResponse(t *testing.T) {
	// The payload is opaque to the client; only the server decodes audio.
	data := bytes.Repeat([]byte("audio"), (1<<20)+1)
	want := &sttv1.MeetingResponse{
		DurationMs: 1200, SpeakerIds: []uint32{1, 2},
		Diarization: []*sttv1.SpeakerInterval{{StartMs: 100, EndMs: 1200, SpeakerId: 1}},
		Segments:    []*sttv1.MeetingSegment{{StartMs: 100, EndMs: 1200, SpeakerIds: []uint32{1, 2}, Text: "Привет, мир!", HasOverlap: true, ForcedSplit: true}},
	}
	address := serveTest(t, func(stream grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error {
		first, err := stream.Recv()
		if err != nil || first.GetHeader() == nil || first.GetHeader().GetSizeBytes() != uint64(len(data)) {
			t.Errorf("invalid header: %v %v", first, err)
			return status.Error(codes.InvalidArgument, "invalid header")
		}
		var received []byte
		chunks := 0
		for {
			request, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			chunk := request.GetChunk()
			if len(chunk) == 0 || len(chunk) > 4<<20 {
				t.Errorf("invalid chunk: %d", len(chunk))
			}
			received = append(received, chunk...)
			chunks++
		}
		if chunks != 2 || !bytes.Equal(received, data) {
			t.Errorf("file changed in transit: chunks=%d bytes=%d", chunks, len(received))
		}
		return stream.SendAndClose(want)
	})
	var out, stderr bytes.Buffer
	path := writeRecording(t, data)
	mergedPath := filepath.Join(filepath.Dir(path), "merged.txt")
	err := run(context.Background(), []string{"-addr", address, "-file", path, "-chunk-bytes", "4194304", "-timeout", "5s", "-merged-out", mergedPath}, &out, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	wantText := "Длительность: 00:00:01.200\nГоворящие: Говорящий 1, Говорящий 2\n\n" +
		"[00:00:00.100 — 00:00:01.200] Говорящий 1, Говорящий 2 | одновременная речь | разрез по лимиту длительности\nПривет, мир!\n\n"
	if out.String() != wantText || stderr.Len() != 0 {
		t.Fatalf("stdout=%s stderr=%s", out.String(), stderr.String())
	}
	saved, err := os.ReadFile(path + ".transcription.json")
	if err != nil {
		t.Fatal(err)
	}
	var got sttv1.MeetingResponse
	if err := protojson.Unmarshal(saved, &got); err != nil || !proto.Equal(want, &got) {
		t.Fatalf("saved JSON: %v; %s", err, saved)
	}
	savedText, err := os.ReadFile(path + ".transcription.txt")
	if err != nil || !bytes.Equal(savedText, out.Bytes()) {
		t.Fatalf("saved text differs from stdout: %v", err)
	}
	merged, err := os.ReadFile(mergedPath)
	wantMerged := "Длительность: 00:00:01\nГоворящие: 1, 2\n\n" +
		"[00:00:00 — 00:00:01] 1, 2 | одновременная речь | разрез по лимиту длительности\nПривет, мир!\n\n"
	if err != nil || string(merged) != wantMerged {
		t.Fatalf("custom merged transcript: %v", err)
	}
}

func TestCLIReceivesResponseLargerThanDefaultGRPCLimit(t *testing.T) {
	want := strings.Repeat("я", (2<<20)+1)
	address := serveTest(t, func(stream grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error {
		for {
			_, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
		return stream.SendAndClose(&sttv1.MeetingResponse{Segments: []*sttv1.MeetingSegment{{Text: want}}})
	})
	var out bytes.Buffer
	output := filepath.Join(t.TempDir(), "result.json")
	if err := run(context.Background(), []string{"-addr", address, "-file", writeRecording(t, []byte("audio")), "-out", output, "-timeout", "5s"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var got sttv1.MeetingResponse
	if err := protojson.Unmarshal(saved, &got); err != nil || len(got.Segments) != 1 || got.Segments[0].Text != want {
		t.Fatalf("large response lost: %v", err)
	}
	if !strings.Contains(out.String(), want) {
		t.Fatal("large readable response lost")
	}
	savedText, err := os.ReadFile(strings.TrimSuffix(output, ".json") + ".txt")
	if err != nil || !bytes.Equal(savedText, out.Bytes()) {
		t.Fatalf("custom result file: %v", err)
	}
	merged, err := os.ReadFile(strings.TrimSuffix(output, ".json") + ".merged.txt")
	if err != nil || string(merged) != formatMergedMeeting(&got) {
		t.Fatalf("default merged transcript: %v", err)
	}
}

func TestCLIRejectsOverwritingAudioWithMergedTranscript(t *testing.T) {
	path := writeRecording(t, []byte("audio"))
	if err := run(context.Background(), []string{"-file", path, "-merged-out", path}, io.Discard, io.Discard); err == nil {
		t.Fatal("source audio accepted as merged output")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "audio" {
		t.Fatalf("source audio changed: %q %v", data, err)
	}
}

func TestCLIReportsServerErrorWithoutSuccessfulOutput(t *testing.T) {
	address := serveTest(t, func(grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error {
		return status.Error(codes.ResourceExhausted, "meeting busy")
	})
	var out bytes.Buffer
	path := writeRecording(t, bytes.Repeat([]byte("a"), 5<<20))
	output := filepath.Join(filepath.Dir(path), "result.json")
	if err := os.WriteFile(output, []byte("previous result"), 0600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"-addr", address, "-file", path, "-out", output, "-timeout", "5s"}, &out, io.Discard)
	if status.Code(err) != codes.ResourceExhausted || out.Len() != 0 {
		t.Fatalf("err=%v stdout=%s", err, out.String())
	}
	saved, err := os.ReadFile(output)
	if err != nil || string(saved) != "previous result" {
		t.Fatalf("failed request replaced previous result: %q %v", saved, err)
	}
	temporary, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".stt-transcription-*"))
	if len(temporary) != 0 {
		t.Fatalf("temporary results leaked: %v", temporary)
	}
}

func TestCLIRejectsOverwritingSourceAudio(t *testing.T) {
	path := writeRecording(t, []byte("audio"))
	err := run(context.Background(), []string{"-file", path, "-out", path}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("source audio accepted as output file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "audio" {
		t.Fatalf("source audio changed: %q %v", data, err)
	}
}

func TestCLICancellationWhileWaitingForResult(t *testing.T) {
	started := make(chan struct{})
	address := serveTest(t, func(stream grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error {
		close(started)
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	var out bytes.Buffer
	err := run(ctx, []string{"-addr", address, "-file", writeRecording(t, []byte("audio"))}, &out, io.Discard)
	if status.Code(err) != codes.Canceled && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("canceled request printed successful response")
	}
}

func TestReadableEmptyMeeting(t *testing.T) {
	got := formatMeeting(&sttv1.MeetingResponse{DurationMs: 9_000_123})
	want := "Длительность: 02:30:00.123\nГоворящие: не определены\n\nРечь не распознана.\n"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestCLIRejectsOverwritingAudioWithText(t *testing.T) {
	path := writeRecording(t, []byte("audio"))
	if err := run(context.Background(), []string{"-file", path, "-text-out", path}, io.Discard, io.Discard); err == nil {
		t.Fatal("source audio accepted as text output")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "audio" {
		t.Fatalf("source audio changed: %q %v", data, err)
	}
}

package transport

import (
	"context"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	sttv1 "vks-stt-v2/gen/stt/v1"
	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/diarization"
	"vks-stt-v2/internal/meeting"
)

type meetingDiarizer struct{}

func (meetingDiarizer) Diarize(context.Context, string) ([]diarization.Segment, error) {
	return []diarization.Segment{{Start: 0, End: audio.SampleRate, Speaker: 1}, {Start: audio.SampleRate / 4, End: audio.SampleRate * 3 / 4, Speaker: 2}}, nil
}

func meetingClient(t *testing.T, decode meeting.DecodeFile) (sttv1.GigaamClient, *meeting.Service) {
	t.Helper()
	s := &meeting.Service{TempDir: t.TempDir(), ChunkBytes: 1 << 20, MaxSeconds: 25, Diarizer: meetingDiarizer{}, Model: model{}, Decode: decode}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer(grpc.MaxRecvMsgSize((1<<20)+1024), grpc.WaitForHandlers(true))
	RegisterGRPC(server, service(), s)
	go server.Serve(listener)
	conn, err := grpc.NewClient("passthrough:///meeting-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(); conn.Close(); server.Stop(); s.Close(); listener.Close() })
	return sttv1.NewGigaamClient(conn), s
}
func meetingHeader(size uint64) *sttv1.MeetingRequest {
	return &sttv1.MeetingRequest{Payload: &sttv1.MeetingRequest_Header{Header: &sttv1.MeetingUploadHeader{SizeBytes: size}}}
}
func meetingChunk(p []byte) *sttv1.MeetingRequest {
	return &sttv1.MeetingRequest{Payload: &sttv1.MeetingRequest_Chunk{Chunk: p}}
}
func decodedSilence(_ context.Context, _, _, out string) (int64, error) {
	return audio.SampleRate, os.WriteFile(out, make([]byte, audio.SampleRate*2), 0600)
}

func TestMeetingGRPCUploadAndOneCompleteResponse(t *testing.T) {
	var calls atomic.Int32
	client, s := meetingClient(t, func(ctx context.Context, ffmpeg, input, out string) (int64, error) {
		calls.Add(1)
		data, err := os.ReadFile(input)
		if err != nil || string(data) != "abcdef" {
			t.Errorf("%q %v", data, err)
		}
		return decodedSilence(ctx, ffmpeg, input, out)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.TranscribeMeeting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []*sttv1.MeetingRequest{meetingHeader(6), meetingChunk([]byte("ab")), meetingChunk([]byte("cdef"))} {
		if err := stream.Send(v); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("pipeline started before upload EOF")
	}
	r, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || r.DurationMs != 1000 || !reflect.DeepEqual(r.SpeakerIds, []uint32{1, 2}) || len(r.Segments) != 1 || !r.Segments[0].HasOverlap || !reflect.DeepEqual(r.Segments[0].SpeakerIds, []uint32{1, 2}) {
		t.Fatalf("%+v", r)
	}
	entries, _ := filepath.Glob(filepath.Join(s.TempDir, "upload-*"))
	if len(entries) != 0 {
		t.Fatal("upload directory leaked")
	}
}

func TestMeetingGRPCInvalidSequencesAndPermitRecovery(t *testing.T) {
	client, _ := meetingClient(t, decodedSilence)
	cases := [][]*sttv1.MeetingRequest{
		nil, {meetingChunk([]byte("a"))}, {meetingHeader(0)},
		{meetingHeader(1), meetingHeader(1)},
		{meetingHeader(2), meetingChunk([]byte("a"))},
		{meetingHeader(1), meetingChunk([]byte("ab"))},
		{meetingHeader(1), meetingChunk(nil)},
	}
	for i, messages := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		stream, err := client.TranscribeMeeting(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range messages {
			_ = stream.Send(v)
		}
		_, err = stream.CloseAndRecv()
		cancel()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	stream, err := client.TranscribeMeeting(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(meetingHeader(1))
	_ = stream.Send(meetingChunk([]byte("x")))
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("permit leaked after invalid upload: %v", err)
	}
}

func TestMeetingRejectsSecondUploadWhilePipelineRuns(t *testing.T) {
	started, unblock := make(chan struct{}), make(chan struct{})
	client, _ := meetingClient(t, func(ctx context.Context, a, b, c string) (int64, error) {
		close(started)
		select {
		case <-unblock:
			return decodedSilence(ctx, a, b, c)
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := client.TranscribeMeeting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Send(meetingHeader(1))
	_ = first.Send(meetingChunk([]byte("x")))
	done := make(chan error, 1)
	go func() { _, err := first.CloseAndRecv(); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, err := client.TranscribeMeeting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.CloseAndRecv()
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second meeting: %v", err)
	}
	close(unblock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMeetingCanceledUploadCleansFilesAndReleasesPermit(t *testing.T) {
	var calls atomic.Int32
	client, s := meetingClient(t, func(ctx context.Context, a, b, c string) (int64, error) {
		calls.Add(1)
		return decodedSilence(ctx, a, b, c)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.TranscribeMeeting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(meetingHeader(1 << 20)); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(meetingChunk([]byte("partial"))); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		files, err := filepath.Glob(filepath.Join(s.TempDir, "upload-*", "input"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if _, err := stream.CloseAndRecv(); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled upload: %v", err)
	}
	// Client cancellation can return before the server has finished its defers.
	for {
		_, release, err := s.Begin(context.Background())
		if err == nil {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("meeting permit leaked after canceled upload")
		}
		time.Sleep(time.Millisecond)
	}
	files, _ := filepath.Glob(filepath.Join(s.TempDir, "upload-*"))
	if len(files) != 0 || calls.Load() != 0 {
		t.Fatalf("canceled upload: files=%v pipeline_calls=%d", files, calls.Load())
	}
}

func TestMeetingUploadLargerThanProtobufMessage(t *testing.T) {
	if os.Getenv("MEETING_LARGE_UPLOAD_TEST") == "" {
		t.Skip("set MEETING_LARGE_UPLOAD_TEST=1 for the >2 GiB disk-backed transport test")
	}
	const chunks = 2049
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	table := crc32.MakeTable(crc32.Castagnoli)
	want := crc32.New(table)
	for i := 0; i < chunks; i++ {
		_, _ = want.Write(data)
	}
	client, _ := meetingClient(t, func(ctx context.Context, a, input, out string) (int64, error) {
		f, err := os.Open(input)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		got := crc32.New(table)
		n, err := io.Copy(got, f)
		if err != nil || n != int64(chunks)*int64(len(data)) || got.Sum32() != want.Sum32() {
			t.Errorf("upload corrupted: bytes=%d crc=%d err=%v", n, got.Sum32(), err)
		}
		return decodedSilence(ctx, a, input, out)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stream, err := client.TranscribeMeeting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(meetingHeader(uint64(chunks) * uint64(len(data)))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < chunks; i++ {
		if err := stream.Send(meetingChunk(data)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatal(err)
	}
}

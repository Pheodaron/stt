package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"syscall"

	sttv1 "vks-stt-v2/gen/stt/v1"
	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/meeting"
	"vks-stt-v2/internal/stt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type GRPC struct {
	sttv1.UnimplementedGigaamServer
	Service *stt.Service
	Meeting *meeting.Service
}

func RegisterGRPC(server *grpc.Server, service *stt.Service, meetings ...*meeting.Service) {
	g := &GRPC{Service: service}
	if len(meetings) > 0 {
		g.Meeting = meetings[0]
	}
	sttv1.RegisterGigaamServer(server, g)
}

func (g *GRPC) TranscribeMeeting(stream grpc.ClientStreamingServer[sttv1.MeetingRequest, sttv1.MeetingResponse]) error {
	if g.Meeting == nil {
		return status.Error(codes.Unavailable, "meeting transcription is unavailable")
	}
	ctx, release, err := g.Meeting.Begin(stream.Context())
	if err != nil {
		return grpcError(err)
	}
	defer release()
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "missing meeting header")
	}
	if err != nil {
		return err
	}
	header := first.GetHeader()
	if header == nil {
		return status.Error(codes.InvalidArgument, "first meeting message must be a header")
	}
	upload, err := g.Meeting.NewUpload(header.GetSizeBytes())
	if err != nil {
		return grpcError(err)
	}
	defer upload.Close()
	for {
		if err := ctx.Err(); err != nil {
			return grpcError(err)
		}
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		chunk, ok := request.Payload.(*sttv1.MeetingRequest_Chunk)
		if !ok {
			return status.Error(codes.InvalidArgument, "expected an audio chunk")
		}
		if err := upload.Write(ctx, chunk.Chunk); err != nil {
			return grpcError(err)
		}
	}
	result, err := upload.Finish(ctx)
	if err != nil {
		return grpcError(err)
	}
	response := meetingProto(result)
	if proto.Size(response) > math.MaxInt32 {
		return status.Error(codes.ResourceExhausted, "meeting response exceeds protobuf message limit")
	}
	if err := ctx.Err(); err != nil {
		return grpcError(err)
	}
	return stream.SendAndClose(response)
}

func meetingProto(r *meeting.Result) *sttv1.MeetingResponse {
	ms := func(n int64) int64 { return n/audio.SampleRate*1000 + (n%audio.SampleRate)*1000/audio.SampleRate }
	out := &sttv1.MeetingResponse{DurationMs: ms(r.Samples), SpeakerIds: r.Speakers}
	for _, v := range r.Diarization {
		out.Diarization = append(out.Diarization, &sttv1.SpeakerInterval{StartMs: ms(v.Start), EndMs: ms(v.End), SpeakerId: v.Speaker})
	}
	for _, v := range r.Segments {
		out.Segments = append(out.Segments, &sttv1.MeetingSegment{StartMs: ms(v.Start), EndMs: ms(v.End), SpeakerIds: v.Speakers, Text: v.Text, HasOverlap: v.Overlap, ForcedSplit: v.ForcedSplit})
	}
	return out
}

func (g *GRPC) TranscribeUnbuffered(ctx context.Context, request *sttv1.VoiceChunk) (*sttv1.TranscribedText, error) {
	session := g.Service.NewSession()
	defer session.Close()
	r, err := session.Transcribe(ctx, request.GetSegment(), true)
	if err != nil {
		return nil, grpcError(err)
	}
	// Keep the original unary method's partial field for existing clients.
	return &sttv1.TranscribedText{Partial: r.Message}, nil
}

func (g *GRPC) Transcribe(stream grpc.BidiStreamingServer[sttv1.VoiceChunk, sttv1.TranscribedText]) error {
	session := g.Service.NewSession()
	defer session.Close()
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			r, err := session.Flush(stream.Context())
			if err != nil {
				return grpcError(err)
			}
			if r != nil {
				return stream.Send(toProto(*r))
			}
			return nil
		}
		if err != nil {
			return err
		}
		r, err := session.Transcribe(stream.Context(), request.GetSegment(), false)
		if err != nil {
			return grpcError(err)
		}
		if err := stream.Send(toProto(r)); err != nil {
			return err
		}
	}
}

func toProto(r stt.Result) *sttv1.TranscribedText {
	return &sttv1.TranscribedText{Partial: r.Partial, Message: r.Message}
}

func grpcError(err error) error {
	switch {
	case errors.Is(err, meeting.ErrBusy), errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, meeting.ErrClosed):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, meeting.ErrUpload):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "recognition timed out")
	case errors.Is(err, audio.ErrLimit):
		return status.Error(codes.ResourceExhausted, "audio limit exceeded")
	case errors.Is(err, audio.ErrAudio):
		return status.Error(codes.InvalidArgument, "invalid audio")
	default:
		slog.Error("recognition failed", "error", err)
		return status.Error(codes.Internal, "recognition failed")
	}
}

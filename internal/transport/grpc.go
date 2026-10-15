package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"

	sttv1 "vks-stt-v2/gen/stt/v1"
	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/stt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPC struct {
	sttv1.UnimplementedGigaamServer
	Service *stt.Service
}

func RegisterGRPC(server *grpc.Server, service *stt.Service) {
	sttv1.RegisterGigaamServer(server, &GRPC{Service: service})
}

func (g *GRPC) TranscribeUnbuffered(ctx context.Context, request *sttv1.VoiceChunk) (*sttv1.TranscribedText, error) {
	r, err := g.Service.NewSession().Transcribe(ctx, request.GetSegment(), true)
	if err != nil {
		return nil, grpcError(err)
	}
	// Keep the original unary method's partial field for existing clients.
	return &sttv1.TranscribedText{Partial: r.Message}, nil
}

func (g *GRPC) Transcribe(stream grpc.BidiStreamingServer[sttv1.VoiceChunk, sttv1.TranscribedText]) error {
	session := g.Service.NewSession()
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if r := session.Flush(); r != nil {
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

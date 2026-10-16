// Package stt is a compile-only stand-in for the protobuf code missing from the
// supplied archive. It is copied ONLY into the temporary validation module.
// It must never replace vks_back/gen/proto/stt in the real server.
package stt

import (
	"context"
	"google.golang.org/grpc"
)

type VoiceChunk struct{ Segment []byte }
type TranscribedText struct {
	Partial *string
	Message *string
}
type GigaamClient interface {
	Transcribe(context.Context, ...grpc.CallOption) (Gigaam_TranscribeClient, error)
}
type Gigaam_TranscribeClient interface {
	Send(*VoiceChunk) error
	Recv() (*TranscribedText, error)
	grpc.ClientStream
}

func NewGigaamClient(grpc.ClientConnInterface) GigaamClient {
	panic("compile-only validation stub: a real STT client requires the original protobuf files")
}

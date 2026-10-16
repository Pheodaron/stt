package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync/atomic"
	"time"
	"vks_back/gen/proto/stt"
	"vks_back/internal/vks/stt/packeter"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v3"
)

var (
	agentCounter atomic.Int32
)

type SttAgent struct {
	room   *lksdk.Room
	client stt.GigaamClient
	ctx    context.Context
	cancel context.CancelFunc
}

type BroadcastMessageData struct {
	User string               `json:"user"`
	Data *stt.TranscribedText `json:"data"`
}

type BroadcastMessage struct {
	Type string                `json:"type"`
	Data *BroadcastMessageData `json:"data"`
}

func NewAgent() *SttAgent {
	ctx, cancel := context.WithCancel(context.Background())
	return &SttAgent{client: NewSttClient(), ctx: ctx, cancel: cancel}
}

func (s *SttAgent) BroadcastTranscript(
	rp *lksdk.RemoteParticipant,
	msg *stt.TranscribedText,
) error {
	payload, err := json.Marshal(&BroadcastMessage{
		Type: "srv:subtitle",
		Data: &BroadcastMessageData{
			User: rp.Identity(),
			Data: msg,
		},
	})
	if err != nil {
		return err
	}

	return s.room.LocalParticipant.PublishDataPacket(&lksdk.UserDataPacket{
		Payload: payload,
		Topic:   "transcript",
	})
}

// WithParticipant sends and receives independently; cancel must cancel the
// context used to open rpcClient, so a failed worker unblocks the other one.
func (s *SttAgent) WithParticipant(
	ctx context.Context,
	cancel context.CancelFunc,
	ch <-chan *bytes.Buffer,
	rp *lksdk.RemoteParticipant,
	rpcClient stt.Gigaam_TranscribeClient,
) error {
	return exchangeChunks(ctx, cancel, ch, rpcClient, func(res *stt.TranscribedText) error {
		if res.Partial != nil {
			log.Printf("partial: %s", *res.Partial)
		}
		if res.Message != nil {
			log.Printf("transcript: %s", *res.Message)
		}
		return s.BroadcastTranscript(rp, res)
	})
}

func AssertIsAudio(track *webrtc.TrackRemote) bool {
	return track.Kind() == webrtc.RTPCodecTypeAudio
}

func (s *SttAgent) OnTrack(
	track *webrtc.TrackRemote,
	pub *lksdk.RemoteTrackPublication,
	rp *lksdk.RemoteParticipant,
) {
	if !AssertIsAudio(track) {
		return
	}

	log.Println("Listen to user: ", rp.Name(), "session: ", pub.Name())
	log.Println("Codec: ", track.Codec())

	// The output queue is deliberately small; enqueue has a bounded wait.
	ch := make(chan *bytes.Buffer, 4)
	packer, err := packeter.NewOpusBuffer(track, ch)
	if err != nil {
		log.Println("packeter.NewOpusBuffer:", err)
		return
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	client, err := s.client.Transcribe(ctx)
	if err != nil {
		log.Println("s.client.Transcribe:", err)
		return
	}
	done := make(chan error, 1)
	go func() { done <- s.WithParticipant(ctx, cancel, ch, rp, client) }()

	packErr := packer.StartContext(ctx)
	// StartContext has already delivered the valid tail on EOF/read failure.
	// Let the sender drain the queue, CloseSend, and receive final responses.
	close(ch)
	// Bound shutdown even if Send is blocked before it can observe close(ch).
	drainTimer := time.NewTimer(transcriptDrainTimeout)
	var rpcErr error
	select {
	case rpcErr = <-done:
	case <-drainTimer.C:
		cancel()
		rpcErr = <-done
		log.Println("STT drain timed out for:", rp.Identity())
	}
	drainTimer.Stop()
	if packErr != nil && !errors.Is(packErr, io.EOF) && !errors.Is(packErr, context.Canceled) {
		log.Println("packeter:", packErr)
	}
	if rpcErr != nil && !errors.Is(rpcErr, context.Canceled) {
		log.Println("transcribe:", rpcErr)
	}
}

func (s *SttAgent) Remove() {
	// Local DisconnectWithReason need not invoke OnDisconnected in the SDK.
	if s.cancel != nil {
		s.cancel()
	}
	if s.room != nil {
		s.room.DisconnectWithReason(livekit.DisconnectReason_CLIENT_INITIATED)
	}
}

func (s *SttAgent) OnDisconnect() {
	if s.cancel != nil {
		s.cancel()
	}
	agentCounter.Add(-1)
}

func (s *SttAgent) PrepareRoomConfig(name string) lksdk.ConnectInfo {
	apiKey := os.Getenv("LIVEKIT_KEY")
	apiSecret := os.Getenv("LIVEKIT_SECRET")
	count := agentCounter.Add(1)

	return lksdk.ConnectInfo{
		APIKey:              apiKey,
		APISecret:           apiSecret,
		RoomName:            name,
		ParticipantName:     fmt.Sprintf("sttbot-%d", count),
		ParticipantIdentity: "sttbot",
		ParticipantKind:     lksdk.ParticipantAgent,
	}
}

func (s *SttAgent) PrepareCb() *lksdk.RoomCallback {
	return &lksdk.RoomCallback{
		OnDisconnected:            s.OnDisconnect,
		OnDisconnectedWithReason:  func(reason lksdk.DisconnectionReason) {},
		OnParticipantConnected:    func(*lksdk.RemoteParticipant) {},
		OnParticipantDisconnected: func(*lksdk.RemoteParticipant) {},
		OnActiveSpeakersChanged:   func([]lksdk.Participant) {},
		OnRoomMetadataChanged:     func(metadata string) {},
		OnReconnecting:            func() {},
		OnReconnected:             func() {},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: s.OnTrack,
		},
	}
}

func (s *SttAgent) JoinRoom(name string) error {
	hostURL := os.Getenv("LIVEKIT_HOST")
	room, err := lksdk.ConnectToRoom(
		hostURL,
		s.PrepareRoomConfig(name),
		s.PrepareCb(),
		lksdk.WithAutoSubscribe(true),
	)
	if err != nil {
		s.cancel()
		return err
	}

	s.room = room
	return nil
}

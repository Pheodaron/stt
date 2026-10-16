package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync/atomic"
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
	return &SttAgent{
		client: NewSttClient(),
	}
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

const (
	maxAudioLate = 200
)

func (s *SttAgent) WithParticipant(
	ch chan *bytes.Buffer,
	rp *lksdk.RemoteParticipant,
	rpcClient stt.Gigaam_TranscribeClient,
) error {
	for buf := range ch {
		log.Println("Sent: ", buf.Len())
		err := rpcClient.Send(&stt.VoiceChunk{
			Segment: buf.Bytes(),
		})
		if err != nil {
			log.Println("rpcClient.Send: ", err)
			return err
		}

		res, err := rpcClient.Recv()
		if err != nil {
			log.Println("rpcClient.Recv: ", err)
			return err
		}

		if res.Partial != nil {
			log.Printf("got: %s\n", *res.Partial)
		}
		if res.Message != nil {
			log.Printf("got: %s\n", *res.Message)
		}
		err = s.BroadcastTranscript(rp, res)
		if err != nil {
			log.Println("s.BroadcastTranscript: ", err)
			return err
		}
	}
	return nil
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

	ch := make(chan *bytes.Buffer, 14)
	defer close(ch)

	decoder, err := packeter.NewOpusBuffer(track, ch)
	if err != nil {
		log.Println("packeter.NewOpusBuffer err: ", err)
		s.Remove()
		return
	}

	client, err := s.client.Transcribe(context.Background())
	if err != nil {
		log.Println("s.client.Transcribe err: ", err)
		s.Remove()
		return
	}

	go s.WithParticipant(ch, rp, client)

	err = decoder.Start()
	if err == io.EOF {
		log.Println("User: ", rp.Name(), " EOF")
		return
	}
	if err != nil {
		log.Println("DECODER ERR: ", err)
		s.Remove()
	}
}

func (s *SttAgent) Remove() {
	// TODO: find out if it triggers OnDisconnect
	s.room.DisconnectWithReason(livekit.DisconnectReason_CLIENT_INITIATED)
}

func (s *SttAgent) OnDisconnect() {
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
		return err
	}

	s.room = room
	return nil
}

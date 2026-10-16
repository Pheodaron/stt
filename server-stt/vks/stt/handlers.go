package stt

import (
	"context"
	"vks_back/internal/config"
	"vks_back/internal/vks/stt/dto"

	"github.com/gin-gonic/gin"
	juju_errors "github.com/juju/errors"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Stt struct {
	roomClient *lksdk.RoomServiceClient
}

func NewSttAgentHandlers() *Stt {
	return &Stt{
		roomClient: config.Livekit(),
	}
}

func (s *Stt) StartStt(g *gin.Context, in *dto.StartSttReq) error {
	agent := NewAgent()
	err := agent.JoinRoom(in.Room)
	if err != nil {
		return juju_errors.NewNotFound(err, "Not found room")
	}
	return nil
}

func (s *Stt) StopStt(g *gin.Context, in *dto.StopSttReq) error {
	botPart := &livekit.RoomParticipantIdentity{
		Room:     in.Room,
		Identity: "sttbot",
	}

	_, err := s.roomClient.GetParticipant(context.Background(), botPart)
	if err != nil {
		//TODO: handle if no such participant
		return err
	}

	_, err = s.roomClient.RemoveParticipant(context.Background(), botPart)
	return err
}

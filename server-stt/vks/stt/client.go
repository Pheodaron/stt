package stt

import (
	"log"
	"os"
	"sync"
	"vks_back/gen/proto/stt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var _NewGrpc = sync.OnceValue(func() stt.GigaamClient {
	addr := os.Getenv("STT_CLIENT")
	log.Println("Try connect stt to:", addr)
	cc, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Println("failed to connect to stt: ", err)
	}
	return stt.NewGigaamClient(cc)
})

func NewSttClient() stt.GigaamClient {
	return _NewGrpc()
}

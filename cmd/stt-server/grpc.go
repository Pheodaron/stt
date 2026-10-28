package main

import (
	"context"
	"math"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"vks-stt-v2/internal/config"
	"vks-stt-v2/internal/meeting"
	"vks-stt-v2/internal/stt"
	"vks-stt-v2/internal/transport"
)

func serveGRPC(ctx context.Context, listener net.Listener, service *stt.Service, cfg config.Config, meetings *meeting.Service) error {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(max(cfg.MaxSegmentBytes, cfg.MeetingChunkBytes)+1024), grpc.MaxSendMsgSize(math.MaxInt32), grpc.WaitForHandlers(true))
	defer server.Stop() // Also cancel handlers if Serve fails before shutdown.
	transport.RegisterGRPC(server, service, meetings)
	reflection.Register(server)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("Gigaam", grpc_health_v1.HealthCheckResponse_SERVING)

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		healthServer.Shutdown()
		if meetings != nil {
			meetings.Stop()
		}
		stopGRPC(server)
		return nil
	}
}

func stopGRPC(server *grpc.Server) {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(shutdownTimeout):
		server.Stop()
		<-stopped
	}
}

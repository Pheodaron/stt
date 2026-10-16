package main

import (
	"context"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"vks-stt-v2/internal/config"
	"vks-stt-v2/internal/stt"
	"vks-stt-v2/internal/transport"
)

func serveGRPC(ctx context.Context, listener net.Listener, service *stt.Service, cfg config.Config) error {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(cfg.MaxSegmentBytes + 1024))
	transport.RegisterGRPC(server, service)
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

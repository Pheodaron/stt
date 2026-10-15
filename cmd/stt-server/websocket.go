package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"vks-stt-v2/internal/config"
	"vks-stt-v2/internal/stt"
	"vks-stt-v2/internal/transport"
)

func serveWebSocket(ctx context.Context, listener net.Listener, service *stt.Service, cfg config.Config) error {
	websocket := transport.NewWebSocket(ctx, service, cfg.MaxSegmentBytes, cfg.PingInterval)
	defer websocket.Close()

	mux := http.NewServeMux()
	mux.Handle("/", websocket)
	mux.HandleFunc("/healthz", healthHandler)

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()

	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err

	case <-ctx.Done():
		websocket.Close()
		return stopHTTP(server)
	}
}

func stopHTTP(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		server.Close()
		return err
	}

	return nil
}

func healthHandler(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Write([]byte("{\"status\":\"ok\"}\n"))
}

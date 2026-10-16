package transport

import (
	"context"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"vks-stt-v2/internal/stt"
)

type WebSocket struct {
	Service         *stt.Service
	MaxSegmentBytes int
	PingInterval    time.Duration

	ctx         context.Context
	mu          sync.Mutex
	connections map[*websocket.Conn]context.CancelFunc
	closed      bool
}

func NewWebSocket(ctx context.Context, service *stt.Service, maxBytes int, ping time.Duration) *WebSocket {
	return &WebSocket{
		Service:         service,
		MaxSegmentBytes: maxBytes,
		PingInterval:    ping,
		ctx:             ctx,
		connections:     make(map[*websocket.Conn]context.CancelFunc),
	}
}

func (w *WebSocket) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closed = true
	for connection, cancel := range w.connections {
		cancel()
		connection.Close()
	}
}

func (w *WebSocket) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// The default origin check permits browser clients from the same host.
	upgrader := websocket.Upgrader{HandshakeTimeout: 10 * time.Second}
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(w.ctx)
	if !w.track(connection, cancel) {
		cancel()
		connection.Close()
		return
	}
	defer w.release(connection, cancel)

	timeout := 2*w.PingInterval + w.Service.Timeout
	connection.SetReadLimit(int64(base64.StdEncoding.EncodedLen(w.MaxSegmentBytes) + 4096))
	connection.SetReadDeadline(time.Now().Add(timeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(timeout))
	})

	go w.ping(ctx, connection, cancel)
	w.readMessages(ctx, connection, timeout)
}

func (w *WebSocket) track(connection *websocket.Conn, cancel context.CancelFunc) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return false
	}

	w.connections[connection] = cancel
	return true
}

func (w *WebSocket) release(connection *websocket.Conn, cancel context.CancelFunc) {
	cancel()
	connection.Close()

	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.connections, connection)
}

func (w *WebSocket) ping(ctx context.Context, connection *websocket.Conn, cancel context.CancelFunc) {
	ticker := time.NewTicker(w.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deadline := time.Now().Add(5 * time.Second)
			if connection.WriteControl(websocket.PingMessage, nil, deadline) != nil {
				cancel()
				connection.Close()
				return
			}
		}
	}
}

func (w *WebSocket) readMessages(ctx context.Context, connection *websocket.Conn, timeout time.Duration) {
	session := w.Service.NewSession()
	defer session.Close()

	for {
		kind, data, err := connection.ReadMessage()
		if err != nil {
			return
		}
		connection.SetReadDeadline(time.Now().Add(timeout))

		var reply []byte
		if kind == websocket.TextMessage {
			reply = w.dispatch(ctx, session, data)
		} else {
			reply = rpcError(nil, -32600, "Invalid Request")
		}
		if reply == nil {
			continue
		}

		connection.SetWriteDeadline(time.Now().Add(w.Service.Timeout))
		if connection.WriteMessage(websocket.TextMessage, reply) != nil {
			return
		}
	}
}

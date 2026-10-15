package transport

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	sttv1 "vks-stt-v2/gen/stt/v1"
	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/stt"
)

type processor struct{}

func (processor) Decode(ctx context.Context, b []byte) ([]byte, error) {
	if string(b) == "bad" {
		return nil, audio.ErrAudio
	}

	if len(b) == 0 {
		return nil, nil
	}

	return make([]byte, 320), nil
}

type passthroughVAD struct{}

func (passthroughVAD) NewStream() (audio.Filter, error) { return &passthroughFilter{}, nil }

type passthroughFilter struct{}

func (*passthroughFilter) Accept(_ context.Context, pcm []byte) (audio.Chunk, error) {
	return audio.Chunk{PCM: pcm}, nil
}
func (*passthroughFilter) Flush(context.Context) (audio.Chunk, error) {
	return audio.Chunk{Ended: true}, nil
}
func (*passthroughFilter) Close() {}

type model struct{}

func (model) Transcribe(context.Context, []byte) (string, error) {
	return "Привет, мир!", nil
}

func service() *stt.Service {
	return &stt.Service{
		Decoder:    processor{},
		VAD:        passthroughVAD{},
		Model:      model{},
		BufferSize: 2,
		OverlapMS:  500,
		MaxSeconds: 25,
		Timeout:    time.Second,
	}
}

func TestGRPCWireCompatibilityAndFinalFlush(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterGRPC(server, service())
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient(
		"passthrough:///test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Invoke the original method path explicitly rather than trusting generated names.
	var unary sttv1.TranscribedText
	err = conn.Invoke(ctx, "/Gigaam/transcribe_unbuffered", &sttv1.VoiceChunk{Segment: []byte("audio")}, &unary)
	if err != nil || unary.Partial == nil || unary.GetPartial() != "Привет, мир!" || unary.Message != nil {
		t.Fatalf("%+v %v", &unary, err)
	}
	err = conn.Invoke(ctx, "/Gigaam/transcribe_unbuffered", &sttv1.VoiceChunk{Segment: []byte("bad")}, &unary)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	stream, err := sttv1.NewGigaamClient(conn).Transcribe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := stream.Send(&sttv1.VoiceChunk{Segment: []byte("audio")}); err != nil {
		t.Fatal(err)
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	partial, err := stream.Recv()
	if err != nil || partial.Partial == nil {
		t.Fatalf("%+v %v", partial, err)
	}
	final, err := stream.Recv()
	if err != nil || final.GetMessage() != "Привет, мир!" {
		t.Fatalf("%+v %v", final, err)
	}

	if _, err := stream.Recv(); err != io.EOF {
		t.Fatal(err)
	}
}

func TestJSONRPCValidationNotificationsAndBatch(t *testing.T) {
	w := NewWebSocket(context.Background(), service(), 1<<20, time.Second)
	for _, tc := range []struct {
		input string
		code  int
	}{
		{"{", -32700},
		{"[]", -32600},
		{"null", -32600},
		{`{"jsonrpc":"2.0","id":{},"method":"flush"}`, -32600},
		{`{"jsonrpc":"2.0","id":1,"method":"missing"}`, -32601},
		{`{"jsonrpc":"2.0","id":1,"method":"upload_part","params":{"segment":"!"}}`, -32602},
		{`{"jsonrpc":"2.0","id":1,"method":"upload_part","params":{}}`, -32602},
		{`{"jsonrpc":"2.0","id":1,"method":"flush","params":["unexpected"]}`, -32602},
	} {
		var r rpcResponse
		if err := json.Unmarshal(w.dispatch(context.Background(), service().NewSession(), []byte(tc.input)), &r); err != nil {
			t.Fatal(err)
		}
		if r.Error == nil || r.Error.Code != tc.code {
			t.Fatalf("%s: %+v", tc.input, r)
		}
	}
	s := service().NewSession()
	notification := []byte(`{"jsonrpc":"2.0","method":"upload_part","params":{"segment":"YQ=="}}`)
	if r := w.dispatch(context.Background(), s, notification); r != nil {
		t.Fatalf("notification replied: %s", r)
	}
	requests := []byte(`[
		{"jsonrpc":"2.0","id":"end","method":"flush"},
		{"jsonrpc":"2.0","method":"flush"}
	]`)
	data := w.dispatch(context.Background(), s, requests)
	var batch []rpcResponse
	if err := json.Unmarshal(data, &batch); err != nil {
		t.Fatalf("%s %v", data, err)
	}
	if len(batch) != 1 || batch[0].Result == nil || batch[0].Result.Message == nil {
		t.Fatalf("missing batch result: %s", data)
	}
	if *batch[0].Result.Message != "Привет, мир!" {
		t.Fatalf("unexpected batch result: %s", data)
	}

	var positional rpcResponse
	request := []byte(`{"jsonrpc":"2.0","id":9,"method":"upload_part_unbuffered","params":["YQ=="]}`)
	data = w.dispatch(context.Background(), s, request)
	if err := json.Unmarshal(data, &positional); err != nil || positional.Result == nil || positional.Result.Message == nil {
		t.Fatalf("positional params: %s %v", data, err)
	}
}

func TestWebSocketConnectionIsolation(t *testing.T) {
	w := NewWebSocket(context.Background(), service(), 1<<20, time.Second)
	defer w.Close()
	srv := httptest.NewServer(w)
	defer srv.Close()
	connect := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		return c
	}
	a, b := connect(), connect()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"upload_part","params":{"segment":"YQ=="}}`)
	for _, c := range []*websocket.Conn{a, b} {
		if err := c.WriteMessage(websocket.TextMessage, request); err != nil {
			t.Fatal(err)
		}
		var r rpcResponse
		if err := c.ReadJSON(&r); err != nil {
			t.Fatal(err)
		}
		if r.Result == nil || r.Result.Partial == nil {
			t.Fatalf("buffer leaked between connections: %+v", r)
		}
	}
}

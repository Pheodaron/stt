package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

type rpcClient struct {
	connection *websocket.Conn
}

func sendWebSocket(ctx context.Context, address string, files []string, unbuffered bool) error {
	if !strings.HasPrefix(address, "ws://") && !strings.HasPrefix(address, "wss://") {
		address = "ws://" + address
	}

	connection, _, err := websocket.DefaultDialer.DialContext(ctx, address, nil)
	if err != nil {
		return err
	}
	defer connection.Close()

	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()

	if deadline, ok := ctx.Deadline(); ok {
		connection.SetReadDeadline(deadline)
		connection.SetWriteDeadline(deadline)
	}

	method := "upload_part"
	if unbuffered {
		method = "upload_part_unbuffered"
	}

	client := rpcClient{connection: connection}
	for index, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		params := map[string]string{"segment": base64.StdEncoding.EncodeToString(data)}
		if err := client.call(index+1, method, params); err != nil {
			return err
		}
	}

	if !unbuffered {
		return client.call(len(files)+1, "flush", map[string]any{})
	}

	return nil
}

func (client rpcClient) call(id int, method string, params any) error {
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	if err := client.connection.WriteJSON(request); err != nil {
		return err
	}

	_, data, err := client.connection.ReadMessage()
	if err != nil {
		return err
	}

	var response struct {
		Error *json.RawMessage `json:"error"`
	}

	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("RPC: %s", *response.Error)
	}

	fmt.Println(string(data))
	return nil
}

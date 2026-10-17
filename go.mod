module vks-stt-v2

go 1.25.1

require (
	github.com/gorilla/websocket v1.5.3
	github.com/k2-fsa/sherpa-onnx-go v1.13.8
	github.com/lmittmann/tint v1.2.1
	google.golang.org/grpc v1.75.1
	google.golang.org/protobuf v1.36.9
)

require (
	github.com/k2-fsa/sherpa-onnx-go-linux v1.13.8 // indirect
	github.com/k2-fsa/sherpa-onnx-go-macos v1.13.8 // indirect
	github.com/k2-fsa/sherpa-onnx-go-windows v1.13.8 // indirect
	golang.org/x/net v0.41.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.26.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250707201910-8d1bb00bc6a7 // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.5.1 // indirect
)

tool (
	google.golang.org/grpc/cmd/protoc-gen-go-grpc
	google.golang.org/protobuf/cmd/protoc-gen-go
)

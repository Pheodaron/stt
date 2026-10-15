#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
protoc -I proto --go_out=. --go_opt=module=vks-stt-v2 --go-grpc_out=. --go-grpc_opt=module=vks-stt-v2 proto/stt/v1/stt.proto

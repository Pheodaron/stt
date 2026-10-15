.PHONY: build build-gpu image-cpu image-gpu model model-int8 run test race vet proto tools bench

build:
	sh script/build.sh

build-gpu: build
	sh script/install-cuda-libs.sh

image-cpu:
	docker build -f Dockerfile.cpu -t vks-stt-go:cpu .

image-gpu:
	docker build -f Dockerfile.gpu -t vks-stt-go:cuda .

model:
	sh script/export-model-fp32.sh

model-int8:
	sh script/download-model-int8.sh

run:
	go run ./cmd/stt-server

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

proto:
	sh script/proto.sh

tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.9
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

bench:
	go run ./cmd/stt-bench -file "$(FILE)" -threads 1,2,4 -runs 5

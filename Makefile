.PHONY: build build-gpu image-cpu image-gpu model model-int8 run test race vet proto tools bench

# Compatibility shortcuts; mise.toml defines the actual commands.

build:
	mise run build

build-gpu:
	mise run build:local-gpu

image-cpu:
	mise run build:image-cpu

image-gpu:
	mise run build:image-gpu

model:
	mise run model

model-int8:
	mise run model:int8

run:
	mise run run

test:
	mise run test

race:
	mise run race

vet:
	mise run vet

proto:
	mise run proto

tools:
	mise run tools

bench:
	mise run bench -- -file "$(FILE)" -threads 1,2,4 -runs 5

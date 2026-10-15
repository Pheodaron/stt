#!/bin/sh
# Native Podman commands work without a Docker socket or Compose provider.
set -eu
cd "$(dirname "$0")/.."

operation=${1:-}
profile=${2:-}
if [ "$#" -lt 2 ]; then
  echo 'Usage: podman.sh build|run|stop|logs|bench cpu|gpu [arguments...]' >&2
  exit 2
fi
shift 2

case "$profile" in
  cpu)
    provider=cpu
    precision=${MODEL_PRECISION:-fp32}
    image=localhost/vks-stt-go:cpu
    ;;
  gpu)
    provider=cuda
    precision=fp32
    image=localhost/vks-stt-go:cuda
    ;;
  *)
    echo 'Profile must be cpu or gpu.' >&2
    exit 2
    ;;
esac

case "$precision" in
  fp32|int8) ;;
  *)
    echo 'MODEL_PRECISION must be fp32 or int8.' >&2
    exit 2
    ;;
esac

case "$operation" in
  build)
    exec podman build --file "Dockerfile.$profile" --tag "$image" "$@" .
    ;;
  stop)
    exec podman stop --time 15 "vks-stt-$profile"
    ;;
  logs)
    exec podman logs --follow "vks-stt-$profile"
    ;;
  run|bench) ;;
  *)
    echo 'Operation must be build, run, stop, logs or bench.' >&2
    exit 2
    ;;
esac

if [ ! -f .env ]; then
  echo 'Create the configuration first: cp .env.example .env' >&2
  exit 1
fi

if [ "$operation" = run ]; then
  set -- --name "vks-stt-$profile" --init \
    --publish "${PORT:-8080}:8080" \
    "$image" "$@"
else
  set -- --entrypoint /app/stt-bench "$image" "$@"
fi

if [ "$profile" = gpu ]; then
  # CDI exposes only the selected GPU. Disable SELinux label separation for it.
  # NVIDIA's legacy hook must not inject the same device a second time.
  set -- --device "nvidia.com/gpu=${GPU_DEVICE_ID:-0}" \
    --security-opt label=disable \
    --env NVIDIA_VISIBLE_DEVICES=void "$@"
fi

exec podman run --rm --pull=never \
  --env-file .env \
  --env URL=0.0.0.0 \
  --env PORT=8080 \
  --env "MODEL_PROVIDER=$provider" \
  --env "MODEL_PRECISION=$precision" \
  --env "MODEL_DIR=/app/models/gigaam-v3-$precision" \
  --volume "$(pwd)/models:/app/models:ro,z" \
  "$@"

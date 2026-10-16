#!/usr/bin/env bash
set -euo pipefail
validation_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source_dir="$(cd -- "$validation_dir/../vks/stt" && pwd)"
go_bin="${GO_BIN:-go}"
"$go_bin" version
command -v ffmpeg >/dev/null || { echo 'FFmpeg is required for the audio decoding regression check.' >&2; exit 1; }
check_dir="$(mktemp -d "${TMPDIR:-/tmp}/stt-validation.XXXXXX")"
trap 'rm -rf -- "$check_dir"' EXIT
mkdir -p "$check_dir/internal/vks/stt" "$check_dir/gen/proto/stt"
cp "$validation_dir/go.mod" "$check_dir/go.mod"
cp "$validation_dir/go.sum" "$check_dir/go.sum"
cp "$validation_dir/stubs/stt.go" "$check_dir/gen/proto/stt/stt.go"
cp "$source_dir/agent.go" "$source_dir/agent_test.go" "$source_dir/client.go" "$source_dir/transcribe.go" "$source_dir/transcribe_test.go" "$check_dir/internal/vks/stt/"
cp -R "$source_dir/packeter" "$check_dir/internal/vks/stt/packeter"
cd -- "$check_dir"
"$go_bin" test -mod=readonly -race -v ./internal/vks/stt/...
"$go_bin" vet -mod=readonly ./internal/vks/stt/...

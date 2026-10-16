#!/bin/sh
# Silero v6.2.3, original unquantized ONNX weights. Shared by CPU/CUDA images.
set -eu
cd "$(dirname "$0")/.."

model_dir=models/silero-v6.2.3
source_url=https://raw.githubusercontent.com/snakers4/silero-vad/v6.2.3
model_sha=1a153a22f4509e292a94e67d6f9b85e8deb25b4988682b7e174c65279d8788e3
license_sha=2e63e9a38b6e8fc0c7bc37ce174caca1862870856c6daf5697cfb785e925520b

verify() {
  printf '%s  %s\n%s  %s\n' \
    "$model_sha" "$1/silero_vad.onnx" \
    "$license_sha" "$1/LICENSE" | sha256sum --check --status
}

if verify "$model_dir" 2>/dev/null; then
  echo "Silero v6.2.3 is present; checksums verified."
  exit 0
fi

command -v curl >/dev/null 2>&1 || {
  echo "Install curl to download Silero VAD." >&2
  exit 1
}
mkdir -p models
download_dir=$(mktemp -d models/.silero-download.XXXXXX)
trap 'rm -rf "$download_dir"' EXIT HUP INT TERM
curl --fail --location --retry 3 --output "$download_dir/silero_vad.onnx" \
  "$source_url/src/silero_vad/data/silero_vad.onnx"
curl --fail --location --retry 3 --output "$download_dir/LICENSE" "$source_url/LICENSE"
if ! verify "$download_dir"; then
  echo "Silero download checksum mismatch." >&2
  exit 1
fi
mkdir -p "$model_dir"
chmod 755 "$model_dir"
chmod 644 "$download_dir/silero_vad.onnx" "$download_dir/LICENSE"
mv "$download_dir/silero_vad.onnx" "$download_dir/LICENSE" "$model_dir/"
echo "Silero v6.2.3 FP32 is ready: $model_dir"

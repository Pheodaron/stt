#!/bin/sh
# Silero v6.2.3, original unquantized ONNX weights. Shared by CPU/CUDA images.
set -eu
cd "$(dirname "$0")/.."

model_dir=models/silero-v6.2.3
source_url=https://github.com/snakers4/silero-vad.git
source_revision=5cd7945676eb32225748052e2e6a0580e4686a08 # v6.2.3
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

command -v git >/dev/null 2>&1 || {
  echo "Install git to download Silero VAD." >&2
  exit 1
}
mkdir -p models
download_dir=$(mktemp -d models/.silero-download.XXXXXX)
trap 'rm -rf "$download_dir"' EXIT HUP INT TERM
# Use GitHub's Git transport instead of raw.githubusercontent.com. Fetch only
# the pinned release and check out the two required files, not the examples.
checkout="$download_dir/source"
git clone --quiet --filter=blob:none --no-checkout --depth 1 --branch v6.2.3 \
  "$source_url" "$checkout"
if [ "$(git -C "$checkout" rev-parse HEAD)" != "$source_revision" ]; then
  echo 'Silero v6.2.3 tag does not match the pinned revision.' >&2
  exit 1
fi
git -C "$checkout" sparse-checkout set --no-cone /LICENSE /src/silero_vad/data/silero_vad.onnx
git -C "$checkout" -c advice.detachedHead=false checkout --quiet --detach "$source_revision"
cp "$checkout/src/silero_vad/data/silero_vad.onnx" "$download_dir/silero_vad.onnx"
cp "$checkout/LICENSE" "$download_dir/LICENSE"
if ! verify "$download_dir"; then
  echo "Silero download checksum mismatch." >&2
  exit 1
fi
mkdir -p "$model_dir"
chmod 755 "$model_dir"
chmod 644 "$download_dir/silero_vad.onnx" "$download_dir/LICENSE"
mv "$download_dir/silero_vad.onnx" "$download_dir/LICENSE" "$model_dir/"
echo "Silero v6.2.3 FP32 is ready: $model_dir"

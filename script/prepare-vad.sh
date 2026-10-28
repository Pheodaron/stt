#!/bin/sh
set -eu
model_dir=${1:-/model/silero-v6.2.3}
source_dir=$(mktemp -d)
trap 'rm -rf "$source_dir"' EXIT HUP INT TERM

git clone --quiet --filter=blob:none --no-checkout --depth 1 --branch v6.2.3 \
  https://github.com/snakers4/silero-vad.git "$source_dir"
[ "$(git -C "$source_dir" rev-parse HEAD)" = 5cd7945676eb32225748052e2e6a0580e4686a08 ]
git -C "$source_dir" sparse-checkout set --no-cone /src/silero_vad/data/silero_vad.onnx
git -C "$source_dir" checkout --quiet --detach HEAD
mkdir -p "$model_dir"
cp "$source_dir/src/silero_vad/data/silero_vad.onnx" "$model_dir/"
printf '%s  %s\n' 1a153a22f4509e292a94e67d6f9b85e8deb25b4988682b7e174c65279d8788e3 \
  "$model_dir/silero_vad.onnx" | sha256sum --check -

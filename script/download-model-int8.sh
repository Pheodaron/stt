#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
destination=${1:-models/gigaam-v3-int8}
package=sherpa-onnx-nemo-transducer-punct-giga-am-v3-russian-2025-12-16
archive_hash=f9620a0099019c6afcee26525ef9ed3297fa50dd5691c1902af0c948fc1a470b
if [ -s "$destination/encoder.int8.onnx" ] && [ -s "$destination/decoder.onnx" ] && [ -s "$destination/joiner.onnx" ] && [ -s "$destination/tokens.txt" ]; then
  echo "Model already exists in $destination"
  exit 0
fi
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
curl --fail --location --retry 3 "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/$package.tar.bz2" -o "$temporary/model.tar.bz2"
printf '%s  %s\n' "$archive_hash" "$temporary/model.tar.bz2" | sha256sum --check -
# Extract only runtime assets, not the upstream Python export/test scripts.
tar -xjf "$temporary/model.tar.bz2" -C "$temporary" "$package/encoder.int8.onnx" "$package/decoder.onnx" "$package/joiner.onnx" "$package/tokens.txt" "$package/LICENSE"
mkdir -p "$destination"
cp "$temporary/$package/"* "$destination/"
chmod 755 models "$destination"
chmod 644 "$destination/encoder.int8.onnx" "$destination/decoder.onnx" "$destination/joiner.onnx" "$destination/tokens.txt" "$destination/LICENSE"
echo "Model installed in $destination"

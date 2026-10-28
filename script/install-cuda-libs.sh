#!/bin/sh
set -eu
[ "$(go env GOOS)/$(go env GOARCH)" = linux/amd64 ] || {
  echo 'CUDA image requires Linux amd64.' >&2
  exit 1
}
package=sherpa-onnx-v1.13.8-cuda-12.x-cudnn-9.x-onnxruntime1.28.2-linux-x64-gpu
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

curl --fail --location --retry 3 \
  "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/$package.tar.bz2" \
  -o "$temporary/runtime.tar.bz2"
printf '%s  %s\n' 2132af1ae8c84c84f86ac091e4311839287851d2bf5e660332ebce3898a1e0b2 \
  "$temporary/runtime.tar.bz2" | sha256sum --check -
tar -xjf "$temporary/runtime.tar.bz2" -C "$temporary" "$package/lib"
rm -f /out/lib/*.so /out/lib/*.so.*
cp -a "$temporary/$package/lib/." /out/lib/
find /out/lib -type f -name '*.so*' -exec chmod 755 {} +

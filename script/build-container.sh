#!/bin/sh
# Used by the build stages in Dockerfile.cpu and Dockerfile.gpu.
set -eu
cd "$(dirname "$0")/.."

case "$(go env GOOS)/$(go env GOARCH)" in
  linux/amd64) target=x86_64-unknown-linux-gnu ;;
  linux/arm64) target=aarch64-unknown-linux-gnu ;;
  *)
    echo 'Container builds support Linux amd64/arm64.' >&2
    exit 1
    ;;
esac

mkdir -p /out/lib
CGO_ENABLED=1 CGO_LDFLAGS="${CGO_LDFLAGS:-} -Wl,-rpath,\$ORIGIN/lib" \
  go build -trimpath -o /out/stt-server ./cmd/stt-server

native_dir=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux)
cp "$native_dir/lib/$target/"*.so /out/lib/
cp "$native_dir/LICENSE" /out/lib/LICENSE.sherpa-onnx
chmod 755 /out /out/lib /out/stt-server /out/lib/*.so
chmod 644 /out/lib/LICENSE.sherpa-onnx

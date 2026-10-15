#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
case "$(go env GOOS)/$(go env GOARCH)" in
  linux/amd64) target=x86_64-unknown-linux-gnu ;;
  linux/arm64) target=aarch64-unknown-linux-gnu ;;
  *) echo 'Packaging supports Linux amd64/arm64; use go build on other supported platforms.' >&2; exit 1 ;;
esac
mkdir -p bin/lib
CGO_ENABLED=1 CGO_LDFLAGS="${CGO_LDFLAGS:-} -Wl,-rpath,\$ORIGIN/lib" go build -trimpath -o bin/ ./cmd/...
native_dir=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux)
cp -f "$native_dir/lib/$target/"*.so bin/lib/
cp -f "$native_dir/LICENSE" bin/lib/LICENSE.sherpa-onnx
chmod 755 bin bin/lib bin/stt-* bin/lib/*.so
chmod 644 bin/lib/LICENSE.sherpa-onnx

#!/bin/sh
# Replace only the generated native-library bundle, keeping the Go C API version.
set -eu
cd "$(dirname "$0")/.."
destination=${1:-bin/lib}
case "$destination" in
  /*) ;;
  *) destination="$(pwd)/$destination" ;;
esac

if [ "$(go env GOOS)/$(go env GOARCH)" != linux/amd64 ]; then
  echo 'The packaged CUDA runtime supports Linux amd64 only.' >&2
  exit 1
fi
if [ "$(go list -m -f '{{.Version}}' github.com/k2-fsa/sherpa-onnx-go)" != v1.13.8 ]; then
  echo 'Update the pinned CUDA runtime together with the sherpa Go API version.' >&2
  exit 1
fi

package=sherpa-onnx-v1.13.8-cuda-12.x-cudnn-9.x-onnxruntime1.28.2-linux-x64-gpu
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
archive=${SHERPA_CUDA_ARCHIVE:-"$temporary/runtime.tar.bz2"}

if [ -z "${SHERPA_CUDA_ARCHIVE:-}" ]; then
  curl --fail --location --retry 3 \
    "https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/$package.tar.bz2" \
    -o "$archive"
fi
printf '%s  %s\n' 2132af1ae8c84c84f86ac091e4311839287851d2bf5e660332ebce3898a1e0b2 "$archive" | sha256sum --check -
tar -xjf "$archive" -C "$temporary" "$package/lib"

for file in libsherpa-onnx-c-api.so libonnxruntime.so libonnxruntime_providers_cuda.so libonnxruntime_providers_shared.so; do
  if [ ! -e "$temporary/$package/lib/$file" ]; then
    echo "Missing CUDA runtime asset: $file" >&2
    exit 1
  fi
done

mkdir -p "$destination"
# CPU/GPU runtimes must not coexist under the same SONAME in this bundle.
rm -f "$destination/"*.so "$destination/"*.so.*
cp -a "$temporary/$package/lib/." "$destination/"
chmod 755 "$destination"
find "$destination" -type f -name '*.so*' -exec chmod 755 {} +
echo "Installed sherpa-onnx 1.13.8 / ONNX Runtime 1.28.2 CUDA 12 + cuDNN 9 in $destination"

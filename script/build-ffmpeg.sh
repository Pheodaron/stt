#!/bin/sh
# Used only in the ffmpeg-build stages; no build tools enter the server image.
set -eu
revision=1041abdc962f4cc4f394aa8de9dc5236c0c3b9e7 # FFmpeg n8.1.3
source_url="https://github.com/FFmpeg/FFmpeg/archive/$revision.tar.gz"
checksum=a406fe831cbb605f5fe50e43eb566e502ae63ff3b3206bfa744e4b31b8bb73ac
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
curl --fail --location --retry 3 "$source_url" -o "$temporary/source.tar.gz"
printf '%s  %s\n' "$checksum" "$temporary/source.tar.gz" | sha256sum --check -
tar -xzf "$temporary/source.tar.gz" --strip-components=1 -C "$temporary"
cd "$temporary"

# Keep native decoders/demuxers. The server only writes PCM16 over a pipe;
# external libraries, network access, other encoders and muxers are unnecessary.
./configure \
  --cc=musl-gcc --extra-ldflags=-static \
  --disable-shared --enable-static --disable-autodetect \
  --disable-programs --enable-ffmpeg --disable-doc --disable-debug --enable-small \
  --disable-network --disable-avdevice --disable-swscale \
  --disable-protocols --enable-protocol=pipe \
  --disable-encoders --enable-encoder=pcm_s16le \
  --disable-muxers --enable-muxer=pcm_s16le \
  --disable-filters --enable-filter=aresample,aformat,anull
make -j"${FFMPEG_BUILD_JOBS:-2}" ffmpeg
strip --strip-unneeded ffmpeg
# Fail the build if a future configuration accidentally adds a dynamic loader.
if readelf --program-headers ffmpeg | grep -q INTERP; then
  echo 'FFmpeg must be a standalone static executable.' >&2
  exit 1
fi
install -D -m 755 ffmpeg /opt/ffmpeg/bin/ffmpeg
mkdir -p /opt/ffmpeg/licenses
cp COPYING.LGPLv2.1 /opt/ffmpeg/licenses/
cp /usr/share/doc/musl/copyright /opt/ffmpeg/licenses/musl.copyright
cp /usr/share/doc/libgcc-s1/copyright /opt/ffmpeg/licenses/libgcc.copyright
printf 'FFmpeg n8.1.3\nSource: %s\nSHA-256: %s\n' "$source_url" "$checksum" \
  > /opt/ffmpeg/licenses/SOURCE
cp /usr/local/bin/build-ffmpeg /opt/ffmpeg/licenses/build-ffmpeg.sh
chmod 755 /opt/ffmpeg /opt/ffmpeg/bin /opt/ffmpeg/licenses
chmod 644 /opt/ffmpeg/licenses/*
/opt/ffmpeg/bin/ffmpeg -version

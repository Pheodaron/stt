#!/bin/sh
# Build only the official native CPU diarization component; no runtime server.
set -eu
. /opt/stt/nemo-version.env
git clone --no-checkout https://github.com/NVIDIA/NeMo-Speech.cpp.git /opt/nemo-source
cd /opt/nemo-source
git checkout --detach "$NEMO_REVISION"
git submodule update --init --depth 1 llama.cpp
scripts/configure.sh cpu-diar \
  -DCMAKE_INSTALL_LIBDIR=lib -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF \
  -DNEMO_SPEECH_BUILD_CLI=OFF -DNEMO_SPEECH_BUILD_MIC_CAPTURE=OFF
cmake --build --preset cpu-diar -j "${NEMO_BUILD_JOBS:-2}"
cmake --install build/cpu-diar --prefix /opt/nemo
test -s /opt/nemo/lib/libnemo_speech_asr_c.so
# The installed SDK also needs SentencePiece; carry the exact build-time ABI
# instead of relying on a different distro's runtime package in the CUDA image.
sentencepiece_lib=$(ldd /opt/nemo/lib/libnemo_speech_asr.so | awk '$1 == "libsentencepiece.so.0" {print $3}')
if [ -n "$sentencepiece_lib" ]; then
  test -f "$sentencepiece_lib"
  cp -L "$sentencepiece_lib" /opt/nemo/lib/libsentencepiece.so.0
fi
mkdir -p /opt/nemo/share/licenses/stt-nemo
cp LICENSE NOTICE /opt/nemo/share/licenses/stt-nemo/
cp /usr/share/doc/libsentencepiece0/copyright /opt/nemo/share/licenses/stt-nemo/sentencepiece.copyright
cp /opt/stt/nemo-version.env /opt/nemo/share/licenses/stt-nemo/

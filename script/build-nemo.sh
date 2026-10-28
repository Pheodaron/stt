#!/bin/sh
set -eu
git clone --no-checkout https://github.com/NVIDIA/NeMo-Speech.cpp.git /opt/nemo-source
cd /opt/nemo-source
git checkout --detach 66d50d91b75a406becd95fbaf39d2c9c13e95d7c
git submodule update --init --depth 1 llama.cpp
scripts/configure.sh cpu-diar \
  -DCMAKE_INSTALL_LIBDIR=lib -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF \
  -DNEMO_SPEECH_BUILD_CLI=OFF -DNEMO_SPEECH_BUILD_MIC_CAPTURE=OFF
cmake --build --preset cpu-diar -j "${NEMO_BUILD_JOBS:-2}"
cmake --install build/cpu-diar --prefix /opt/nemo
cp -L /usr/lib/$(gcc -dumpmachine)/libsentencepiece.so.0 /opt/nemo/lib/

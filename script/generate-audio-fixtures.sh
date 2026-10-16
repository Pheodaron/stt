#!/bin/sh
# Run in a container with a full FFmpeg installation (including encoders).
# The compact server binary only encodes PCM16 and cannot generate these fixtures.
set -eu
cd "$(dirname "$0")/.."
directory=internal/audio/testdata
mkdir -p "$directory"
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i 'sine=frequency=440:sample_rate=48000:duration=0.25' \
  -ac 2 -map_metadata -1 -c:a pcm_s16le "$directory/stereo48.wav"
encode() {
  name=$1
  codec=$2
  shift 2
  ffmpeg -hide_banner -loglevel error -y -i "$directory/stereo48.wav" \
    -map_metadata -1 -c:a "$codec" "$@" "$directory/$name"
}
encode opus.ogg libopus
encode vorbis.ogg libvorbis
encode tone.mp3 libmp3lame
encode tone.flac flac
encode aac.m4a aac -movflags +faststart
encode alac.m4a alac -movflags +faststart
encode opus.webm libopus

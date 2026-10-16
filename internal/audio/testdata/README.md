Audio fixtures contain a synthetic 440 Hz sine wave, 250 ms long, stereo, 48 kHz.
They contain no recorded speech or third-party audio.

Regenerate them with `sh script/generate-audio-fixtures.sh` inside a disposable
container with a full FFmpeg build and the libopus, libvorbis and libmp3lame
encoders. The server's compact FFmpeg intentionally only encodes PCM16.

The files cover PCM WAV, Ogg/Opus, Ogg/Vorbis, MP3, FLAC, AAC/MP4, ALAC/MP4
and Opus/WebM. `TestFFmpegAudioFormats` feeds each file through stdin and checks
that decoding produces non-silent mono PCM16 at 16 kHz with the expected duration.

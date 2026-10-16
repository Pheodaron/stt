package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestSamples(t *testing.T) {
	pcm := []byte{0, 128, 0, 0, 255, 127}
	x := Samples(pcm)
	if len(x) != 3 || x[0] != -1 || x[1] != 0 || x[2] != float32(32767)/32768 {
		t.Fatal(x)
	}
}

func TestFFmpegDecodeAndLimits(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// A small self-contained WAV verifies that no WAV header reaches VAD/inference.
	pcm := make([]byte, SampleRate*2)
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	for _, v := range []uint16{1, 1} {
		binary.Write(&wav, binary.LittleEndian, v)
	}

	for _, v := range []uint32{SampleRate, SampleRate * 2} {
		binary.Write(&wav, binary.LittleEndian, v)
	}

	for _, v := range []uint16{2, 16} {
		binary.Write(&wav, binary.LittleEndian, v)
	}
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	p := Processor{FFmpeg: "ffmpeg", MaxBytes: 1 << 20, MaxSeconds: 1}
	result, err := p.Decode(context.Background(), wav.Bytes())
	if err != nil || !bytes.Equal(result, pcm) {
		t.Fatalf("decoded PCM mismatch: len=%d err=%v", len(result), err)
	}

	if _, err := p.Decode(context.Background(), []byte("not audio")); !errors.Is(err, ErrAudio) {
		t.Fatal(err)
	}
	p.MaxBytes = 1
	if _, err := p.Decode(context.Background(), wav.Bytes()); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	p.MaxBytes = 1 << 20
	longWAV := append(append([]byte(nil), wav.Bytes()...), pcm...)
	binary.LittleEndian.PutUint32(longWAV[4:8], uint32(36+2*len(pcm)))
	binary.LittleEndian.PutUint32(longWAV[40:44], uint32(2*len(pcm)))
	if _, err := p.Decode(context.Background(), longWAV); !errors.Is(err, ErrLimit) {
		t.Fatalf("decoded duration limit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Decode(ctx, wav.Bytes()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFFmpegAudioFormats(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// Each fixture is 250 ms of a 440 Hz tone, stereo, 48 kHz. Decode uses
	// stdin exactly as the server does, including for MP4 and WebM containers.
	for _, name := range []string{
		"stereo48.wav", "opus.ogg", "vorbis.ogg", "tone.mp3",
		"tone.flac", "aac.m4a", "alac.m4a", "opus.webm",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			p := Processor{FFmpeg: "ffmpeg", MaxBytes: 1 << 20, MaxSeconds: 1}
			pcm, err := p.Decode(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			// Lossy codecs may include a short priming/padding interval. A wide
			// bound catches failed resampling/downmixing without assuming a codec.
			ms := len(pcm) * 1000 / (SampleRate * 2)
			if ms < 200 || ms > 375 {
				t.Fatalf("expected about 250 ms of mono PCM16 at 16 kHz, got %d ms", ms)
			}
			peak := float32(0)
			for _, sample := range Samples(pcm) {
				if sample < 0 {
					sample = -sample
				}
				if sample > peak {
					peak = sample
				}
			}
			if peak < 0.01 || peak > 0.3 {
				t.Fatalf("decoded tone has invalid amplitude: %f", peak)
			}
		})
	}
}

package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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

func TestVADSilenceAndInputValidation(t *testing.T) {
	for _, ms := range []int{10, 20, 30} {
		c, err := Filter(make([]byte, SampleRate*2), ms, ms*20, 3)
		if err != nil || len(c.PCM) != 0 || c.Map != "" {
			t.Fatalf("%d: %+v %v", ms, c, err)
		}
	}

	if _, err := Filter([]byte{0}, 10, 200, 3); err == nil {
		t.Fatal("odd PCM accepted")
	}

	if _, err := Filter(nil, 15, 200, 3); err == nil {
		t.Fatal("invalid frame duration accepted")
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

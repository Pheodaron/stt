package audio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFileDecoderAudioFormats(t *testing.T) {
	ffmpeg := os.Getenv("FILE_TEST_FFMPEG")
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			t.Skip("ffmpeg not installed")
		}
	}
	for _, name := range []string{"stereo48.wav", "opus.ogg", "vorbis.ogg", "tone.mp3", "tone.flac", "aac.m4a", "alac.m4a", "opus.webm"} {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "decoded.pcm")
			n, err := DecodeFile(context.Background(), ffmpeg, filepath.Join("testdata", name), output)
			if err != nil {
				t.Fatal(err)
			}
			pcm, err := os.ReadFile(output)
			if err != nil || int64(len(pcm)) != 2*n || n < SampleRate/5 || n > SampleRate*3/8 {
				t.Fatalf("invalid decoded duration: samples=%d bytes=%d err=%v", n, len(pcm), err)
			}
			var peak float32
			for _, v := range Samples(pcm) {
				peak = max(peak, v, -v)
			}
			if peak < 0.01 || peak > 0.3 {
				t.Fatalf("invalid decoded amplitude: %v", peak)
			}
		})
	}
}

func TestFileDecoderPreservesSilenceAndExceedsLiveLimit(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "long.wav")
	output := filepath.Join(dir, "decoded.pcm")
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "26", "-c:a", "pcm_s16le", input)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	// The minimal production binary has no lavfi generator. Keep fixture
	// generation separate so the exact container decoder can be checked too.
	if decoder := os.Getenv("FILE_TEST_FFMPEG"); decoder != "" {
		ffmpeg = decoder
	}
	n, err := DecodeFile(context.Background(), ffmpeg, input, output)
	if err != nil {
		t.Fatal(err)
	}
	if n != 26*SampleRate {
		t.Fatalf("silence trimmed or timeline changed: %d", n)
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() != n*2 {
		t.Fatalf("%v %v", info, err)
	}
}

package audio

import (
	"bytes"
	"context"
	"os"
	"testing"
)

type scriptedDetector struct {
	flags  []bool
	index  int
	resets int
}

func (d *scriptedDetector) Speech([]float32) bool {
	var speech bool
	if d.index < len(d.flags) {
		speech = d.flags[d.index]
	}
	d.index++
	return speech
}
func (d *scriptedDetector) Reset() { d.resets++; d.index = 0 }
func (d *scriptedDetector) Close() {}

func TestVADPreservesOnsetAndPartialFrameTail(t *testing.T) {
	detector := &scriptedDetector{flags: []bool{false, false, true, true, true, true}}
	f := &streamFilter{detector: detector, padding: 2 * vadWindowSamples * 2}
	ctx := context.Background()
	pcm := bytes.Repeat([]byte{7, 1}, 4*vadWindowSamples+17)
	// The phrase begins before detection, spans many tiny network chunks, and
	// ends on an incomplete model frame. Every real sample must survive once.
	var got []byte
	for offset := 0; offset < len(pcm); {
		end := min(offset+142, len(pcm))
		chunk, err := f.Accept(ctx, pcm[offset:end])
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, chunk.PCM...)
		offset = end
	}
	tail, err := f.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, tail.PCM...)
	if !bytes.Equal(got, pcm) {
		t.Fatalf("phrase clipped/duplicated: got %d samples, want %d", len(got)/2, len(pcm)/2)
	}
	if detector.resets != 1 {
		t.Fatal("model state reset at a network chunk boundary")
	}
}

func TestVADFlushDetectsShortTailWithoutAddingSyntheticSilence(t *testing.T) {
	// Simulate a model that confirms the last short word on lookahead.
	f := &streamFilter{
		detector: &scriptedDetector{flags: []bool{false, false, true}},
		padding:  10000,
	}
	pcm := bytes.Repeat([]byte{3, 2}, vadWindowSamples+123)
	chunk, err := f.Accept(context.Background(), pcm)
	if err != nil || len(chunk.PCM) != 0 {
		t.Fatal("expected pending onset", err)
	}
	tail, err := f.Flush(context.Background())
	if err != nil || !bytes.Equal(tail.PCM, pcm) || !tail.Ended {
		t.Fatalf("EOF lost the final word or added fake audio: %d != %d (%v)", len(tail.PCM), len(pcm), err)
	}
}

func TestVADSilenceEndpointAndValidation(t *testing.T) {
	f := &streamFilter{
		detector: &scriptedDetector{flags: []bool{true, true, false, false}},
		padding:  1024,
	}
	if _, err := f.Accept(context.Background(), []byte{1}); err == nil {
		t.Fatal("odd PCM accepted")
	}
	chunk, err := f.Accept(context.Background(), make([]byte, 4096))
	if err != nil || !chunk.Ended || len(chunk.PCM) != 2048 {
		t.Fatalf("unexpected endpoint: %+v %v", chunk, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Accept(ctx, make([]byte, 1024)); err == nil {
		t.Fatal("canceled input accepted")
	}
}

// Optional real-model regression: VAD_TEST_MODEL and VAD_TEST_AUDIO point to
// downloaded Silero weights and a speech WAV; no network access occurs in tests.
func TestRealSileroChunkBoundaries(t *testing.T) {
	modelPath := os.Getenv("VAD_TEST_MODEL")
	if modelPath == "" {
		t.Skip("set VAD_TEST_MODEL to test actual Silero inference")
	}
	vad, err := NewSilero(VADConfig{Model: modelPath, Threshold: 0.35, PaddingMS: 250, MinSilenceMS: 500})
	if err != nil {
		t.Fatal(err)
	}
	silent, err := vad.NewStream()
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	chunk, err := silent.Accept(context.Background(), make([]byte, SampleRate*2))
	if err != nil || len(chunk.PCM) != 0 {
		t.Fatal("silence detected as speech", err)
	}
	if tail, err := silent.Flush(context.Background()); err != nil || len(tail.PCM) != 0 {
		t.Fatal("silent flush emitted audio", err)
	}
	path := os.Getenv("VAD_TEST_AUDIO")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := (Processor{FFmpeg: "ffmpeg", MaxBytes: 4 << 20, MaxSeconds: 25}).Decode(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	filterAudio := func(step int) []byte {
		t.Helper()
		stream, err := vad.NewStream()
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		var out []byte
		for offset := 0; offset < len(pcm); offset += step {
			chunk, err := stream.Accept(context.Background(), pcm[offset:min(offset+step, len(pcm))])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, chunk.PCM...)
		}
		tail, err := stream.Flush(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return append(out, tail.PCM...)
	}
	whole := filterAudio(len(pcm))
	if len(whole) < len(pcm)/2 {
		t.Fatal("most speech was lost")
	}
	for _, step := range []int{142, 638, 3200, 31998} {
		if got := filterAudio(step); !bytes.Equal(got, whole) {
			t.Fatalf("network chunk size changed retained speech: step=%d whole=%d chunked=%d", step, len(whole), len(got))
		}
	}
	t.Logf("retained %d/%d samples; all chunkings identical", len(whole)/2, len(pcm)/2)
	original := pcm
	pcm = append(make([]byte, SampleRate*2), pcm...)
	pcm = append(pcm, make([]byte, SampleRate*2)...)
	withPauses := filterAudio(638)
	if !bytes.Contains(withPauses, original) {
		t.Fatal("adding silence around speech clipped its beginning or end")
	}
}

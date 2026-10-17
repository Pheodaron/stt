package stt_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/config"
	"vks-stt-v2/internal/gigaam"
	"vks-stt-v2/internal/stt"
)

// This regression uses real FFmpeg, Silero and GigaAM without downloading
// anything. Set MODEL_TEST_AUDIO, MODEL_TEST_EXPECT and VAD_TEST_MODEL to opt in.
func TestRealChunkedPipeline(t *testing.T) {
	path, vadPath := os.Getenv("MODEL_TEST_AUDIO"), os.Getenv("VAD_TEST_MODEL")
	if path == "" || vadPath == "" {
		t.Skip("set MODEL_TEST_AUDIO and VAD_TEST_MODEL for the complete pipeline")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	model, err := gigaam.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	vad, err := audio.NewSilero(audio.VADConfig{
		Model: vadPath, Threshold: 0.35, PaddingMS: 250, MinSilenceMS: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoder := audio.Processor{FFmpeg: config.FFmpegPath, MaxBytes: 4 << 20, MaxSeconds: 25}
	service := &stt.Service{
		Decoder: decoder, VAD: vad, Model: model,
		BufferSize: 64, OverlapMS: 500, MaxSeconds: 25, Timeout: time.Minute,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pcm, err := decoder.Decode(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []int{len(pcm), 1307 * audio.SampleRate / 1000 * 2} {
		session := service.NewSession()
		var messages []string
		for offset := 0; offset < len(pcm); offset += step {
			result, err := session.Transcribe(ctx, pcmWAV(pcm[offset:min(offset+step, len(pcm))]), false)
			if err != nil {
				session.Close()
				t.Fatal(err)
			}
			if result.Message != nil && *result.Message != "" {
				messages = append(messages, *result.Message)
			}
		}
		final, err := session.Flush(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if final != nil && final.Message != nil {
			messages = append(messages, *final.Message)
		}
		text := strings.Join(messages, " ")
		if text == "" {
			t.Fatal("pipeline returned no final text")
		}
		for _, expected := range strings.Split(os.Getenv("MODEL_TEST_EXPECT"), ",") {
			if !strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(expected))) {
				t.Fatalf("step=%d: expected %q in final text: %q", step, expected, text)
			}
		}
		t.Logf("step=%d samples: %s", step/2, text)
	}
}

func pcmWAV(pcm []byte) []byte {
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	for _, value := range []uint16{1, 1} {
		binary.Write(&wav, binary.LittleEndian, value)
	}
	for _, value := range []uint32{audio.SampleRate, audio.SampleRate * 2} {
		binary.Write(&wav, binary.LittleEndian, value)
	}
	for _, value := range []uint16{2, 16} {
		binary.Write(&wav, binary.LittleEndian, value)
	}
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}

// stt-bench measures warm GigaAM inference, excluding FFmpeg and VAD.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/config"
	"vks-stt-v2/internal/gigaam"
)

type benchmarkResult struct {
	Provider       string    `json:"provider"`
	Precision      string    `json:"precision"`
	Encoder        string    `json:"encoder"`
	Threads        int       `json:"threads"`
	Runs           int       `json:"runs"`
	AudioSeconds   float64   `json:"audio_seconds"`
	LoadSeconds    float64   `json:"load_seconds"`
	MedianSeconds  float64   `json:"median_seconds"`
	RTF            float64   `json:"rtf"`
	ElapsedSeconds []float64 `json:"elapsed_seconds"`
	Text           string    `json:"text"`
}

func main() {
	file := flag.String("file", "", "Audio file, max 25 seconds")
	threads := flag.String("threads", "1,2,4", "Comma separated thread counts")
	runs := flag.Int("runs", 5, "Measured runs after one warm-up")
	flag.Parse()

	if *file == "" || *runs < 1 {
		log.Fatal("provide -file and positive -runs")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := run(ctx, *file, *threads, *runs); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, file, threadList string, runs int) error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}

	pcm, err := readAudio(ctx, cfg, file)
	if err != nil {
		return err
	}

	for _, raw := range strings.Split(threadList, ",") {
		threads, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return err
		}

		cfg.NumThreads = threads
		cfg.ModelWorkers = 1
		if err := cfg.Validate(); err != nil {
			return err
		}

		result, err := measure(ctx, cfg, pcm, runs)
		if err != nil {
			return err
		}

		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
	}

	return nil
}

func readAudio(ctx context.Context, cfg config.Config, file string) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	processor := audio.Processor{
		FFmpeg:     cfg.FFmpeg,
		MaxBytes:   64 << 20,
		MaxSeconds: cfg.MaxAudioSeconds,
	}
	pcm, err := processor.Decode(ctx, data)
	if err != nil {
		return nil, err
	}

	if len(pcm) == 0 {
		return nil, fmt.Errorf("empty audio")
	}

	return pcm, nil
}

func measure(ctx context.Context, cfg config.Config, pcm []byte, runs int) (benchmarkResult, error) {
	started := time.Now()
	model, err := gigaam.New(cfg)
	if err != nil {
		return benchmarkResult{}, err
	}
	defer model.Close()

	result := benchmarkResult{
		Provider:       cfg.Provider,
		Precision:      cfg.Precision,
		Encoder:        cfg.Encoder,
		Threads:        cfg.NumThreads,
		Runs:           runs,
		AudioSeconds:   float64(len(pcm)) / float64(audio.SampleRate*2),
		LoadSeconds:    time.Since(started).Seconds(),
		ElapsedSeconds: make([]float64, runs),
	}

	// Warm up kernels and allocations before recording any inference timings.
	if _, err := model.Transcribe(ctx, pcm); err != nil {
		return benchmarkResult{}, err
	}

	for index := range result.ElapsedSeconds {
		started = time.Now()
		result.Text, err = model.Transcribe(ctx, pcm)
		if err != nil {
			return benchmarkResult{}, err
		}
		result.ElapsedSeconds[index] = time.Since(started).Seconds()
	}

	result.MedianSeconds = median(result.ElapsedSeconds)
	result.RTF = result.MedianSeconds / result.AudioSeconds
	return result, nil
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2

	if len(sorted)%2 == 0 {
		return (sorted[middle-1] + sorted[middle]) / 2
	}

	return sorted[middle]
}

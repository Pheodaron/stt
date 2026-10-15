package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

func splitAudio(ctx context.Context, input, directory string, seconds float64) ([]string, error) {
	output := filepath.Join(directory, "%08d.ogg")
	command := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-i", input,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "libopus",
		"-f", "segment",
		"-segment_time", fmt.Sprint(seconds),
		"-reset_timestamps", "1",
		output,
	)
	command.Stderr = os.Stderr

	if err := command.Run(); err != nil {
		return nil, err
	}

	files, err := filepath.Glob(filepath.Join(directory, "*.ogg"))
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no audio segments produced")
	}

	sort.Strings(files)
	return files, nil
}

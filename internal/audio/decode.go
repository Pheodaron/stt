package audio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

func (p Processor) Decode(ctx context.Context, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	if len(data) > p.MaxBytes {
		return nil, ErrLimit
	}
	cmd := exec.CommandContext(ctx, p.FFmpeg,
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "1",
		"-protocol_whitelist", "pipe",
		"-i", "pipe:0",
		"-vn",
		"-f", "s16le",
		"-ac", "1",
		"-ar", "16000",
		"-acodec", "pcm_s16le",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(data)
	out := &limitedBuffer{max: p.MaxSeconds * SampleRate * 2}
	stderr := &limitedBuffer{max: 8192, discard: true}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if out.exceeded {
			return nil, ErrLimit
		}
		return nil, fmt.Errorf("%w: ffmpeg: %s (%v)", ErrAudio, stderr.String(), err)
	}

	if out.exceeded {
		return nil, ErrLimit
	}

	if out.Len()%2 != 0 {
		return nil, ErrAudio
	}

	return out.Bytes(), nil
}

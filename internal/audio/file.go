package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type fileOutput struct {
	ctx     context.Context
	dst     io.Writer
	written int64
	err     error
}

func (w *fileOutput) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	n, err := w.dst.Write(p)
	w.written += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}

// DecodeFile preserves the decoded timeline, including silence. Unlike Decode,
// its output is disk-backed and is not subject to the live ASR window limit.
func DecodeFile(ctx context.Context, ffmpeg, input, output string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sink := &fileOutput{ctx: ctx, dst: f}
	stderr := &limitedBuffer{max: 8192, discard: true}
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-filter_threads", "1",
		"-protocol_whitelist", "file,pipe", "-i", input, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-f", "s16le", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le", "pipe:1")
	cmd.Stdout, cmd.Stderr = sink, stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if sink.err != nil {
			return 0, fmt.Errorf("write decoded audio: %w", sink.err)
		}
		return 0, fmt.Errorf("%w: ffmpeg: %s (%v)", ErrAudio, stderr.String(), err)
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if sink.written == 0 || sink.written%2 != 0 {
		return 0, ErrAudio
	}
	return sink.written / 2, nil
}

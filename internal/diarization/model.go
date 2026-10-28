// Package diarization runs Nemotron locally using the official NeMo-Speech C ABI.
package diarization

/*
#cgo CFLAGS: -I${SRCDIR}/sdk
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"unsafe"

	"vks-stt-v2/internal/audio"
)

// Segment uses half-open sample offsets in the original mono 16 kHz recording.
// Speaker IDs are 1-based and local to one recording.
type Segment struct {
	Start, End int64
	Speaker    uint32
}

type Model struct {
	mu  sync.Mutex
	api *C.stt_nemo
}

func New(library, path string) (*Model, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("Nemotron model: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("invalid Nemotron model %q", path)
	}
	lib, model := C.CString(library), C.CString(path)
	defer C.free(unsafe.Pointer(lib))
	defer C.free(unsafe.Pointer(model))
	var message [1024]C.char
	api := C.stt_nemo_create(lib, model, &message[0], C.size_t(len(message)))
	if api == nil {
		return nil, fmt.Errorf("Nemotron: %s", C.GoString(&message[0]))
	}
	return &Model{api: api}, nil
}

func (m *Model) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.api != nil {
		C.stt_nemo_destroy(m.api)
		m.api = nil
	}
}

func (m *Model) Diarize(ctx context.Context, path string) ([]Segment, error) {
	m.mu.Lock()
	defer m.mu.Unlock() // Close must wait for every native call.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.api == nil {
		return nil, fmt.Errorf("Nemotron model is closed")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var message [1024]C.char
	nativeErr := func() error { return fmt.Errorf("Nemotron: %s", C.GoString(&message[0])) }
	s := C.stt_nemo_open(m.api, &message[0], C.size_t(len(message)))
	if s == nil {
		return nil, nativeErr()
	}
	defer C.stt_nemo_close(m.api, s)
	buf := make([]byte, audio.SampleRate*2)
	var samples int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, readErr
		}
		if n%2 != 0 {
			return nil, audio.ErrAudio
		}
		if n != 0 {
			pcm := audio.Samples(buf[:n])
			status := C.stt_nemo_push(m.api, s, (*C.float)(unsafe.Pointer(&pcm[0])), C.size_t(len(pcm)), &message[0], C.size_t(len(message)))
			if status != 0 {
				return nil, nativeErr()
			}
			samples += int64(len(pcm))
		}
		if readErr != nil {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if C.stt_nemo_finish(m.api, s, &message[0], C.size_t(len(message))) != 0 {
		return nil, nativeErr()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var ptr *C.nemo_speech_diar_segment
	var count C.size_t
	if C.stt_nemo_segments(m.api, s, &ptr, &count, &message[0], C.size_t(len(message))) != 0 {
		return nil, nativeErr()
	}
	defer C.free(unsafe.Pointer(ptr))
	if uint64(count) > uint64(math.MaxInt)/uint64(C.sizeof_nemo_speech_diar_segment) {
		return nil, fmt.Errorf("too many Nemotron segments")
	}
	result := make([]Segment, 0, int(count))
	for _, v := range unsafe.Slice(ptr, int(count)) {
		start, end := float64(v.start_time), float64(v.end_time)
		if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end < start || end > float64(samples)/audio.SampleRate+1 {
			return nil, fmt.Errorf("invalid Nemotron interval")
		}
		if v.speaker < 1 || v.speaker > 8 {
			return nil, fmt.Errorf("invalid Nemotron speaker")
		}
		a, b := int64(math.Round(start*audio.SampleRate)), min(samples, int64(math.Round(end*audio.SampleRate)))
		if b > a {
			result = append(result, Segment{Start: a, End: b, Speaker: uint32(v.speaker)})
		}
	}
	return result, ctx.Err()
}

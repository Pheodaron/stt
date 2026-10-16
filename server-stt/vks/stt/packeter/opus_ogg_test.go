package packeter

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpusDurations(t *testing.T) {
	for _, tc := range []struct {
		payload []byte
		want    int
	}{
		{[]byte{0}, 480}, {[]byte{8}, 960}, {[]byte{16}, 1920}, {[]byte{24}, 2880},
		{[]byte{0x60}, 480}, {[]byte{0x68}, 960}, {[]byte{0x80}, 120}, {[]byte{0x88}, 240}, {[]byte{0x90}, 480}, {[]byte{0x98}, 960},
		{[]byte{0x99}, 1920}, {[]byte{0x9b, 6}, 5760},
	} {
		got, err := opusSamples(tc.payload)
		if err != nil || got != tc.want {
			t.Errorf("%x: got %d, %v; want %d", tc.payload, got, err, tc.want)
		}
	}
	for _, bad := range [][]byte{nil, {0x9b}, {0x9b, 0}, {0x9b, 7}} {
		if _, err := opusSamples(bad); err == nil {
			t.Errorf("accepted %x", bad)
		}
	}
}

func oggPayloads(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var packets [][]byte
	var current []byte
	for len(data) > 0 {
		if len(data) < 27 || string(data[:4]) != "OggS" {
			t.Fatal("invalid Ogg page")
		}
		n := int(data[26])
		if len(data) < 27+n {
			t.Fatal("truncated lacing")
		}
		size := 27 + n
		for _, lace := range data[27 : 27+n] {
			size += int(lace)
		}
		if len(data) < size {
			t.Fatal("truncated Ogg payload")
		}
		page := append([]byte(nil), data[:size]...)
		want := binary.LittleEndian.Uint32(page[22:])
		setOggCRC(page)
		if got := binary.LittleEndian.Uint32(page[22:]); got != want {
			t.Fatalf("bad CRC: got %x want %x", got, want)
		}
		offset := 27 + n
		for _, lace := range data[27 : 27+n] {
			current = append(current, data[offset:offset+int(lace)]...)
			offset += int(lace)
			if lace < 255 {
				packets = append(packets, current)
				current = nil
			}
		}
		data = data[size:]
	}
	return packets
}

func TestOggHasCorrectHeadersDurationAndEOS(t *testing.T) {
	w := newOpusOgg(1)
	payload := bytes.Repeat([]byte{1}, 255)
	payload[0] = 0xf8
	for i := 0; i < 3; i++ {
		if err := w.writePacket(payload); err != nil {
			t.Fatal(err)
		}
	}
	data := w.finish().Bytes()
	packets := oggPayloads(t, data)
	if len(packets) != 5 {
		t.Fatalf("got %d Ogg packets", len(packets))
	}
	if binary.LittleEndian.Uint16(packets[0][10:]) != 0 {
		t.Fatal("audio would be discarded at every boundary")
	}
	last := data[w.lastPage:]
	if last[5]&4 == 0 {
		t.Fatal("missing EOS")
	}
	if got := binary.LittleEndian.Uint64(last[6:]); got != 3*960 {
		t.Fatalf("wrong granule %d", got)
	}
}

// An independent decoder verifies the real media container, including the last
// short chunk. Optional locally; the validation script requires FFmpeg.
func TestOggChunksDecodeWithoutLosingSamples(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	fixture, err := os.ReadFile("testdata/tone.ogg")
	if err != nil {
		t.Fatal(err)
	}
	packets := oggPayloads(t, fixture)
	var total, decoded int
	for start := 2; start < len(packets); start += 5 {
		end := start + 5
		if end > len(packets) {
			end = len(packets)
		}
		w := newOpusOgg(1)
		for _, payload := range packets[start:end] {
			if err := w.writePacket(payload); err != nil {
				t.Fatal(err)
			}
		}
		total += int(w.samples)
		file := filepath.Join(t.TempDir(), "chunk.ogg")
		if err := os.WriteFile(file, w.finish().Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(ffmpeg, "-v", "error", "-i", file, "-f", "s16le", "-ac", "1", "-ar", "48000", "pipe:1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		pcm, err := cmd.Output()
		if err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, stderr.String())
		}
		decoded += len(pcm) / 2
	}
	if decoded != total {
		t.Fatalf("decoded %d samples, expected %d", decoded, total)
	}
}

package packeter

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

// Use Pion's reader (including its CRC checks), rather than a local Ogg parser.
// Both Pion output and the fixture have one complete audio packet per page.
func readOggAudio(t *testing.T, data []byte) (*oggreader.OggHeader, [][]byte) {
	t.Helper()
	reader, header, err := oggreader.NewWith(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tags, _, err := reader.ParseNextPage()
	if err != nil || !bytes.HasPrefix(tags, []byte("OpusTags")) {
		t.Fatalf("missing OpusTags: %v", err)
	}
	var packets [][]byte
	for {
		payload, _, err := reader.ParseNextPage()
		if errors.Is(err, io.EOF) {
			return header, packets
		}
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, payload)
	}
}

func opusFixture(t *testing.T) [][]byte {
	t.Helper()
	data, err := os.ReadFile("testdata/tone.ogg")
	if err != nil {
		t.Fatal(err)
	}
	_, packets := readOggAudio(t, data)
	// FFmpeg produces 22 packets for this tone, including encoder padding.
	// A different page layout must not silently combine multiple RTP payloads.
	if len(packets) != 22 {
		t.Fatalf("fixture must have 22 single-packet audio pages, got %d", len(packets))
	}
	return packets
}

func TestPionChunksPreservePayloadsAndLibraryHeaderDefaults(t *testing.T) {
	payloads := opusFixture(t)
	track := &fakeTrack{events: make(chan readEvent, len(payloads))}
	output := make(chan *bytes.Buffer, len(payloads))
	_, done := startPacketer(t, track, output, Options{FlushInterval: time.Hour, MaxBufferBytes: 1})
	for i, payload := range payloads {
		p := packet(uint16(i + 1))
		p.Payload = payload
		track.events <- readEvent{packet: p}
	}
	close(track.events)
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("packeter did not finish")
	}

	// Compare against the maintained writer's defaults; do not patch pre-skip,
	// granules, page flags or checksums in the application.
	var reference bytes.Buffer
	writer, err := oggwriter.NewWith(&reference, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteRTP(&rtp.Packet{Payload: payloads[0]}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	wantHeader, _ := readOggAudio(t, reference.Bytes())
	if len(output) != len(payloads) {
		t.Fatalf("got %d chunks, want %d", len(output), len(payloads))
	}
	for i, want := range payloads {
		header, packets := readOggAudio(t, getChunk(t, output).Bytes())
		if *header != *wantHeader {
			t.Fatalf("chunk %d changed Pion header defaults: got %+v, want %+v", i, header, wantHeader)
		}
		if len(packets) != 1 || !bytes.Equal(packets[0], want) {
			t.Fatalf("chunk %d changed or lost its Opus payload", i)
		}
	}
}

// Exercise the actual packeter output with an independent decoder. Pion owns
// pre-skip and granule semantics; this is not a sample-exact boundary test.
func TestPionChunkDecodesWithFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	payloads := opusFixture(t)
	track := &fakeTrack{events: make(chan readEvent, len(payloads))}
	output := make(chan *bytes.Buffer, 1)
	_, done := startPacketer(t, track, output, Options{FlushInterval: time.Hour})
	for i, payload := range payloads {
		p := packet(uint16(i + 1))
		p.Payload = payload
		track.events <- readEvent{packet: p}
	}
	close(track.events)
	chunk := getChunk(t, output)
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "ogg", "-i", "pipe:0", "-f", "s16le", "-ac", "1", "-ar", "48000", "pipe:1")
	cmd.Stdin = bytes.NewReader(chunk.Bytes())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pcm, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, stderr.String())
	}
	if len(pcm) == 0 {
		t.Fatal("FFmpeg decoded no audio")
	}
}

package packeter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync/atomic"
)

var oggSerial atomic.Uint32

// opusOgg muxes complete RTP Opus payloads into an independent Ogg stream.
// There is no new encoder lookahead to discard at a transport boundary, so
// pre-skip is zero. Granules count actual decoded samples, not RTP/DTX gaps.
type opusOgg struct {
	data     []byte
	serial   uint32
	page     uint32
	lastPage int
	samples  uint64
	packets  int
}

func newOpusOgg(channels uint16) *opusOgg {
	w := &opusOgg{serial: oggSerial.Add(1)}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8], head[9] = 1, byte(channels)
	binary.LittleEndian.PutUint32(head[12:], 48000)
	w.appendPage(head, 0, 2)
	var tags bytes.Buffer
	tags.WriteString("OpusTags")
	binary.Write(&tags, binary.LittleEndian, uint32(len("vks-stt")))
	tags.WriteString("vks-stt")
	binary.Write(&tags, binary.LittleEndian, uint32(0))
	w.appendPage(tags.Bytes(), 0, 0)
	return w
}

func (w *opusOgg) writePacket(payload []byte) error {
	// One complete Opus packet per page; a page supports at most 255 laces.
	if len(payload) > 65024 {
		return fmt.Errorf("Opus packet exceeds single Ogg page")
	}
	samples, err := opusSamples(payload)
	if err != nil {
		return err
	}
	w.samples += uint64(samples)
	w.appendPage(payload, w.samples, 0)
	w.packets++
	return nil
}

func (w *opusOgg) appendPage(payload []byte, granule uint64, flags byte) {
	laces := len(payload)/255 + 1
	page := make([]byte, 27+laces+len(payload))
	copy(page, "OggS")
	page[5] = flags
	binary.LittleEndian.PutUint64(page[6:], granule)
	binary.LittleEndian.PutUint32(page[14:], w.serial)
	binary.LittleEndian.PutUint32(page[18:], w.page)
	page[26] = byte(laces)
	for i := 0; i < laces-1; i++ {
		page[27+i] = 255
	}
	page[27+laces-1] = byte(len(payload) % 255)
	copy(page[27+laces:], payload)
	setOggCRC(page)
	w.lastPage = len(w.data)
	w.data = append(w.data, page...)
	w.page++
}

func (w *opusOgg) finish() *bytes.Buffer {
	last := w.data[w.lastPage:]
	last[5] |= 4 // End of logical stream, with an updated page checksum.
	setOggCRC(last)
	return bytes.NewBuffer(w.data)
}

func setOggCRC(page []byte) {
	binary.LittleEndian.PutUint32(page[22:], 0)
	var crc uint32
	for _, b := range page {
		crc ^= uint32(b) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	binary.LittleEndian.PutUint32(page[22:], crc)
}

// Opus TOC duration rules (RFC 6716 sections 3.1 and 3.2), at 48 kHz.
func opusSamples(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, fmt.Errorf("empty Opus packet")
	}
	toc := payload[0]
	var perFrame int
	switch {
	case toc&0x80 != 0:
		perFrame = 120 << ((toc >> 3) & 3)
	case toc&0x60 == 0x60:
		perFrame = 480 << ((toc >> 3) & 1)
	default:
		shift := (toc >> 3) & 3
		perFrame = 480 << shift
		if shift == 3 {
			perFrame = 2880
		}
	}
	frames := 1
	switch toc & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(payload) < 2 {
			return 0, fmt.Errorf("truncated Opus frame count")
		}
		frames = int(payload[1] & 0x3f)
	}
	if frames == 0 || perFrame*frames > 5760 {
		return 0, fmt.Errorf("invalid Opus duration")
	}
	return perFrame * frames, nil
}

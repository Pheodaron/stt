package audio

import "bytes"

type limitedBuffer struct {
	buffer   bytes.Buffer
	max      int
	discard  bool
	exceeded bool
}

func (b *limitedBuffer) Len() int       { return b.buffer.Len() }
func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedBuffer) String() string { return b.buffer.String() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.max-b.Len() {
		b.exceeded = true
		written, _ := b.buffer.Write(p[:b.max-b.Len()])
		if b.discard {
			return n, nil
		}
		return written, ErrLimit
	}

	return b.buffer.Write(p)
}

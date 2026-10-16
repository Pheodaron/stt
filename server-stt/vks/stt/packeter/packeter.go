package packeter

import (
	"bytes"
	"io"
	"time"

	"github.com/livekit/server-sdk-go/v2/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	"github.com/pkg/errors"
)

type PacketCb func(buf []byte) error

type BufferedPacketer struct {
	sb       *samplebuilder.SampleBuilder
	track    *webrtc.TrackRemote
	buf      *bytes.Buffer
	ch       chan *bytes.Buffer
	eofTimes uint
}

const (
	maxAudioLate = 200 // 4s for audio
	buffSize     = 10000
	maxEof       = 8
)

func NewOpusBuffer(
	track *webrtc.TrackRemote,
	ch chan *bytes.Buffer,
) (*BufferedPacketer, error) {
	var buf bytes.Buffer
	return &BufferedPacketer{
		sb:    samplebuilder.New(maxAudioLate, &codecs.OpusPacket{}, track.Codec().ClockRate),
		track: track,
		ch:    ch,
		buf:   &buf,
	}, nil
}

// XXX: do more nice
func (t *BufferedPacketer) Start() error {
	w, err := oggwriter.NewWith(t.buf, 48000, 2)
	if err != nil {
		return err
	}

	for {
		pkt, _, err := t.track.ReadRTP()
		if err == io.EOF {
			if t.eofTimes <= maxEof {
				time.Sleep(4 * time.Second)
				t.eofTimes++
				continue
			} else {
				return io.EOF
			}
		}
		if err != nil {
			return errors.Wrap(err, "track.ReadRTP")
		}
		t.sb.Push(pkt)

		for _, p := range t.sb.PopPackets() {
			err = w.WriteRTP(p)
			if err != nil {
				return errors.Wrap(err, "w.WriteRTP")
			}
		}

		if t.buf.Len() >= buffSize {
			var bufCopy bytes.Buffer
			_, err = io.Copy(&bufCopy, t.buf)
			if err != nil {
				return err
			}

			t.ch <- &bufCopy
			t.buf.Reset()

			w, err = oggwriter.NewWith(t.buf, 48000, 2)
			if err != nil {
				return err
			}
		}
	}
}

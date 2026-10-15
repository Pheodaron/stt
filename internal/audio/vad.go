package audio

import (
	"fmt"
	"runtime"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

type vadFrame struct {
	pcm    []byte
	speech bool
}

type vadCollector struct {
	padding   int
	window    []vadFrame
	triggered bool
	pcm       []byte
	flags     []byte
}

// Filter retains speech with the original >90% trigger/release thresholds.
// Its map has exactly one character per retained PCM frame.
func Filter(pcm []byte, frameMS, paddingMS, mode int) (Chunk, error) {
	if frameMS != 10 && frameMS != 20 && frameMS != 30 {
		return Chunk{}, fmt.Errorf("invalid VAD frame duration")
	}
	if paddingMS < frameMS || paddingMS%frameMS != 0 || len(pcm)%2 != 0 {
		return Chunk{}, ErrAudio
	}

	detector, err := webrtcvad.New()
	if err != nil {
		return Chunk{}, err
	}
	defer runtime.KeepAlive(detector)

	if err := detector.SetMode(mode); err != nil {
		return Chunk{}, err
	}

	collector := vadCollector{padding: paddingMS / frameMS}
	frameBytes := SampleRate * frameMS / 1000 * 2

	for offset := 0; offset+frameBytes <= len(pcm); offset += frameBytes {
		frame := pcm[offset : offset+frameBytes]
		speech, err := detector.Process(SampleRate, frame)
		if err != nil {
			return Chunk{}, err
		}

		collector.push(vadFrame{pcm: frame, speech: speech})
	}

	return Chunk{PCM: collector.pcm, Map: string(collector.flags)}, nil
}

func (c *vadCollector) push(frame vadFrame) {
	c.window = append(c.window, frame)
	if len(c.window) > c.padding {
		copy(c.window, c.window[1:])
		c.window = c.window[:c.padding]
	}

	voiced := c.voicedFrames()
	if !c.triggered {
		if voiced*10 > c.padding*9 {
			c.triggered = true
			for _, previous := range c.window {
				c.retain(previous)
			}
			c.window = c.window[:0]
		}
		return
	}

	c.retain(frame)
	unvoiced := len(c.window) - voiced
	if unvoiced*10 > c.padding*9 {
		c.triggered = false
		c.window = c.window[:0]
	}
}

func (c *vadCollector) voicedFrames() int {
	count := 0
	for _, frame := range c.window {
		if frame.speech {
			count++
		}
	}

	return count
}

func (c *vadCollector) retain(frame vadFrame) {
	c.pcm = append(c.pcm, frame.pcm...)
	if frame.speech {
		c.flags = append(c.flags, '1')
	} else {
		c.flags = append(c.flags, '0')
	}
}

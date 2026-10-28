package audio

import "context"

// FindPause finds a cut inside the latest >=96 ms non-speech run. Unlike the
// live filter this returns a sample offset and never compacts the input audio.
func (v *Silero) FindPause(ctx context.Context, pcm []byte) (int, error) {
	filter, err := v.NewStream()
	if err != nil {
		return 0, err
	}
	defer filter.Close()
	detector := filter.(*streamFilter).detector
	quietStart, quietCount, cut := -1, 0, 0
	for offset := 0; offset+vadWindowSamples*2 <= len(pcm); offset += vadWindowSamples * 2 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if detector.Speech(Samples(pcm[offset : offset+vadWindowSamples*2])) {
			quietStart = -1
			quietCount = 0
		} else {
			if quietStart < 0 {
				quietStart = offset / 2
			}
			quietCount++
			if quietCount >= 3 {
				cut = quietStart + quietCount*vadWindowSamples/2
			}
		}
	}
	return cut, nil
}

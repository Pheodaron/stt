package meeting

import (
	"fmt"
	"slices"
	"sort"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/diarization"
)

type Span struct {
	Start, End int64
	Speakers   []uint32
	Overlap    bool
}

func speakerSet(counts [9]int) []uint32 {
	var ids []uint32
	for id := 1; id <= 8; id++ {
		if counts[id] > 0 {
			ids = append(ids, uint32(id))
		}
	}
	return ids
}

// activity partitions overlapping native intervals without duplicating audio.
func activity(intervals []diarization.Segment, samples int64) ([]Span, error) {
	type event struct {
		at      int64
		speaker uint32
		change  int
	}
	events := make([]event, 0, len(intervals)*2)
	for _, v := range intervals {
		if v.Start < 0 || v.End <= v.Start || v.End > samples || v.Speaker < 1 || v.Speaker > 8 {
			return nil, fmt.Errorf("invalid diarization interval: %+v", v)
		}
		events = append(events, event{v.Start, v.Speaker, 1}, event{v.End, v.Speaker, -1})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at < events[j].at })
	var counts [9]int
	var result []Span
	var previous int64
	for i := 0; i < len(events); {
		at := events[i].at
		ids := speakerSet(counts)
		if at > previous && len(ids) > 0 {
			if len(result) > 0 && result[len(result)-1].End == previous && slices.Equal(result[len(result)-1].Speakers, ids) {
				result[len(result)-1].End = at
			} else {
				result = append(result, Span{Start: previous, End: at, Speakers: ids, Overlap: len(ids) > 1})
			}
		}
		for i < len(events) && events[i].at == at {
			counts[events[i].speaker] += events[i].change
			i++
		}
		previous = at
	}
	return result, nil
}

func union(a, b []uint32) []uint32 {
	ids := append(slices.Clone(a), b...)
	slices.Sort(ids)
	return slices.Compact(ids)
}

// group keeps short backchannels in context where the combined fragment fits.
// The swallowed spans are not independently submitted to ASR.
func group(base []Span, limit int64) []Span {
	var result []Span
	for i := 0; i < len(base); i++ {
		v := base[i]
		if v.Overlap && v.End-v.Start < audio.SampleRate && len(result) > 0 && i+1 < len(base) {
			prev, next := &result[len(result)-1], base[i+1]
			if prev.End == v.Start && v.End == next.Start && next.End-prev.Start <= limit {
				prev.End = next.End
				prev.Speakers = union(union(prev.Speakers, v.Speakers), next.Speakers)
				prev.Overlap = true
				i++
				continue
			}
		}
		if len(result) > 0 {
			prev := &result[len(result)-1]
			if v.Start-prev.End <= audio.SampleRate/4 && slices.Equal(prev.Speakers, v.Speakers) {
				prev.End = v.End
				continue
			}
		}
		result = append(result, v)
	}
	return result
}

func describe(base []Span, start, end int64) ([]uint32, bool) {
	i := sort.Search(len(base), func(i int) bool { return base[i].End > start })
	var ids []uint32
	overlap := false
	for ; i < len(base) && base[i].Start < end; i++ {
		ids = union(ids, base[i].Speakers)
		overlap = overlap || base[i].Overlap
	}
	return ids, overlap
}

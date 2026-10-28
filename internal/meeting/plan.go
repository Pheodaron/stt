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

// splitShortOverlaps assigns each half of a short two-speaker overlap to one speaker.
func splitShortOverlaps(base []Span) []Span {
	var result []Span
	for i, v := range base {
		if len(v.Speakers) != 2 || v.End-v.Start >= audio.SampleRate {
			result = append(result, v)
			continue
		}
		first, second := v.Speakers[0], v.Speakers[1]
		// Continue the preceding speaker into the first half where possible.
		if i > 0 && base[i-1].End == v.Start && len(base[i-1].Speakers) == 1 {
			if base[i-1].Speakers[0] == second {
				first, second = second, first
			}
		} else if i+1 < len(base) && base[i+1].Start == v.End && len(base[i+1].Speakers) == 1 && base[i+1].Speakers[0] == first {
			first, second = second, first
		}
		mid := v.Start + (v.End-v.Start)/2
		if mid > v.Start {
			result = append(result, Span{Start: v.Start, End: mid, Speakers: []uint32{first}})
		}
		result = append(result, Span{Start: mid, End: v.End, Speakers: []uint32{second}})
	}
	return result
}

// speakerIntervals keeps the returned diarization consistent with ASR attribution.
func speakerIntervals(base []Span) []diarization.Segment {
	var result []diarization.Segment
	var last [9]int
	for _, v := range base {
		for _, id := range v.Speakers {
			if n := last[id]; n > 0 && result[n-1].End == v.Start {
				result[n-1].End = v.End
			} else {
				result = append(result, diarization.Segment{Start: v.Start, End: v.End, Speaker: id})
				last[id] = len(result)
			}
		}
	}
	return result
}

// group joins nearby spans with the same speakers.
func group(base []Span) []Span {
	var result []Span
	for _, v := range base {
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

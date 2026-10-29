package meeting

import (
	"reflect"
	"testing"

	"vks-stt-v2/internal/audio"
)

func TestGroupKeepsSpeakerChangesOverlapAndWindowLimit(t *testing.T) {
	span := func(start, end int64, ids ...uint32) Span {
		return Span{Start: start * audio.SampleRate, End: end * audio.SampleRate, Speakers: ids, Overlap: len(ids) > 1}
	}
	for _, tc := range []struct {
		name string
		in   []Span
		want []Span
	}{
		{"same speaker across pauses", []Span{span(1, 3, 1), span(4, 6, 1), span(7, 9, 1)}, []Span{span(1, 9, 1)}},
		{"speaker intervenes", []Span{span(1, 3, 1), span(4, 6, 2), span(7, 9, 1)}, []Span{span(1, 3, 1), span(4, 6, 2), span(7, 9, 1)}},
		{"overlap intervenes", []Span{span(1, 3, 1), span(3, 5, 1, 2), span(5, 7, 1)}, []Span{span(1, 3, 1), span(3, 5, 1, 2), span(5, 7, 1)}},
		{"overlaps stay separate", []Span{span(1, 3, 1, 2), span(3, 5, 1, 2)}, []Span{span(1, 3, 1, 2), span(3, 5, 1, 2)}},
		{"merged window bounded", []Span{span(1, 10, 1), span(11, 21, 1), span(22, 28, 1)}, []Span{span(1, 21, 1), span(22, 28, 1)}},
		{"long silence not submitted", []Span{span(1, 3, 1), span(90, 92, 1)}, []Span{span(1, 3, 1), span(90, 92, 1)}},
		{"long turn left for splitting", []Span{span(1, 60, 1), span(61, 63, 1)}, []Span{span(1, 60, 1), span(61, 63, 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]Span(nil), tc.in...)
			if got := group(tc.in, 24*audio.SampleRate); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v; want %+v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.in, original) {
				t.Fatal("group changed diarization spans")
			}
		})
	}
}

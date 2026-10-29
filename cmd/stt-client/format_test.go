package main

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	sttv1 "vks-stt-v2/gen/stt/v1"
)

func TestMergedTranscriptKeepsLinesAndOriginalIntervals(t *testing.T) {
	response := &sttv1.MeetingResponse{DurationMs: 979139, SpeakerIds: []uint32{1}, Segments: []*sttv1.MeetingSegment{
		{StartMs: 966991, EndMs: 970299, SpeakerIds: []uint32{1}, Text: "Они не просто копировали"},
		{StartMs: 970691, EndMs: 971719, SpeakerIds: []uint32{1}, Text: "А не."},
		{StartMs: 972221, EndMs: 979139, SpeakerIds: []uint32{1}, Text: "Учили своих студентов во всех университетах мира, возвращали их на хорошие условия."},
	}}
	original := proto.Clone(response)
	want := "Длительность: 00:16:19\nГоворящие: 1\n\n" +
		"[00:16:06 — 00:16:19] 1\n" +
		"Они не просто копировали\nА не.\nУчили своих студентов во всех университетах мира, возвращали их на хорошие условия.\n\n"
	if got := formatMergedMeeting(response); got != want {
		t.Fatalf("merged transcript:\n%s", got)
	}
	detailed := formatMeeting(response)
	for _, interval := range []string{
		"[00:16:06.991 — 00:16:10.299]",
		"[00:16:10.691 — 00:16:11.719]",
		"[00:16:12.221 — 00:16:19.139]",
	} {
		if !strings.Contains(detailed, interval) {
			t.Fatalf("missing interval %s:\n%s", interval, detailed)
		}
	}
	if !proto.Equal(original, response) {
		t.Fatal("formatting modified the server response")
	}
}

func TestMergedTranscriptSeparatesSpeakersAndOverlap(t *testing.T) {
	response := &sttv1.MeetingResponse{Segments: []*sttv1.MeetingSegment{
		{StartMs: 0, EndMs: 1000, SpeakerIds: []uint32{1}, Text: "Первый"},
		{StartMs: 1100, EndMs: 2000, SpeakerIds: []uint32{1}, Text: "Продолжение", ForcedSplit: true},
		{StartMs: 2000, EndMs: 3000, SpeakerIds: []uint32{2}, Text: "Второй"},
		{StartMs: 3000, EndMs: 4000, SpeakerIds: []uint32{1, 2}, Text: "Оба", HasOverlap: true},
		{StartMs: 4000, EndMs: 5000, SpeakerIds: []uint32{1}, Text: "Первый снова"},
		{StartMs: 5000, EndMs: 6000},
		{StartMs: 6000, EndMs: 7000},
	}}
	got := formatMergedMeeting(response)
	if strings.Count(got, "[00:") != 6 || !strings.Contains(got, "[00:00:00 — 00:00:02] 1 | разрез по лимиту длительности\nПервый\nПродолжение\n") ||
		!strings.Contains(got, "[00:00:03 — 00:00:04] 1, 2 | одновременная речь") ||
		strings.Count(got, "[текст не распознан]") != 2 {
		t.Fatalf("incorrect grouping:\n%s", got)
	}
}

func TestMergedTranscriptTruncatesMilliseconds(t *testing.T) {
	response := &sttv1.MeetingResponse{Segments: []*sttv1.MeetingSegment{
		{StartMs: 6684200, EndMs: 6686858, SpeakerIds: []uint32{1}, Text: "Спасибо. Спасибо, всего доброго."},
		{StartMs: 6686970, EndMs: 6687758, SpeakerIds: []uint32{3}, Text: "До свидания."},
	}}
	want := "[01:51:24 — 01:51:26] 1\nСпасибо. Спасибо, всего доброго.\n\n" +
		"[01:51:26 — 01:51:27] 3\nДо свидания.\n\n"
	if got := formatMergedMeeting(response); !strings.HasSuffix(got, want) {
		t.Fatalf("merged transcript:\n%s", got)
	}
}

package main

import (
	"fmt"
	"strings"

	sttv1 "vks-stt-v2/gen/stt/v1"
)

func formatMeeting(response *sttv1.MeetingResponse) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Длительность: %s\nГоворящие: %s\n\n", timestamp(response.GetDurationMs()), speakers(response.GetSpeakerIds()))
	if len(response.GetSegments()) == 0 {
		out.WriteString("Речь не распознана.\n")
	}
	for _, segment := range response.GetSegments() {
		fmt.Fprintf(&out, "[%s — %s] %s", timestamp(segment.GetStartMs()), timestamp(segment.GetEndMs()), speakers(segment.GetSpeakerIds()))
		if segment.GetHasOverlap() {
			out.WriteString(" | одновременная речь")
		}
		if segment.GetForcedSplit() {
			out.WriteString(" | разрез по лимиту длительности")
		}
		text := strings.TrimSpace(segment.GetText())
		if text == "" {
			text = "[текст не распознан]"
		}
		fmt.Fprintf(&out, "\n%s\n\n", text)
	}
	return out.String()
}

func speakers(ids []uint32) string {
	if len(ids) == 0 {
		return "не определены"
	}
	labels := make([]string, len(ids))
	for i, id := range ids {
		labels[i] = fmt.Sprintf("Говорящий %d", id)
	}
	return strings.Join(labels, ", ")
}

func timestamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

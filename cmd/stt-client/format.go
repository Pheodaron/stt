package main

import (
	"fmt"
	"strings"

	sttv1 "vks-stt-v2/gen/stt/v1"
)

func formatMeeting(response *sttv1.MeetingResponse) string {
	return formatTranscript(response, false)
}

func formatMergedMeeting(response *sttv1.MeetingResponse) string {
	return formatTranscript(response, true)
}

func formatTranscript(response *sttv1.MeetingResponse, merge bool) string {
	var out strings.Builder
	formatTime := timestamp
	if merge {
		formatTime = timestampSeconds
	}
	fmt.Fprintf(&out, "Длительность: %s\nГоворящие: %s\n\n", formatTime(response.GetDurationMs()), speakers(response.GetSpeakerIds(), merge))
	segments := response.GetSegments()
	if len(segments) == 0 {
		out.WriteString("Речь не распознана.\n")
	}
	for i := 0; i < len(segments); {
		first, last := segments[i], segments[i]
		end := i + 1
		forced := first.GetForcedSplit()
		if merge && !first.GetHasOverlap() && len(first.GetSpeakerIds()) == 1 {
			for end < len(segments) {
				next := segments[end]
				if next.GetHasOverlap() || len(next.GetSpeakerIds()) != 1 || next.GetSpeakerIds()[0] != first.GetSpeakerIds()[0] || next.GetStartMs() < last.GetEndMs() {
					break
				}
				last = next
				forced = forced || next.GetForcedSplit()
				end++
			}
		}
		fmt.Fprintf(&out, "[%s — %s] %s", formatTime(first.GetStartMs()), formatTime(last.GetEndMs()), speakers(first.GetSpeakerIds(), merge))
		if first.GetHasOverlap() {
			out.WriteString(" | одновременная речь")
		}
		if forced {
			out.WriteString(" | разрез по лимиту длительности")
		}
		out.WriteByte('\n')
		for _, segment := range segments[i:end] {
			text := strings.TrimSpace(segment.GetText())
			if text == "" {
				text = "[текст не распознан]"
			}
			fmt.Fprintln(&out, text)
		}
		out.WriteByte('\n')
		i = end
	}
	return out.String()
}

func speakers(ids []uint32, compact bool) string {
	if len(ids) == 0 {
		return "не определены"
	}
	labels := make([]string, len(ids))
	for i, id := range ids {
		labels[i] = fmt.Sprintf("Говорящий %d", id)
		if compact {
			labels[i] = fmt.Sprint(id)
		}
	}
	return strings.Join(labels, ", ")
}

func timestamp(ms int64) string {
	return fmt.Sprintf("%s.%03d", timestampSeconds(ms), ms%1000)
}

func timestampSeconds(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d", ms/3600000, ms/60000%60, ms/1000%60)
}

package scenariodoc

import (
	"strconv"
	"strings"
)

// ptr строит JSON Pointer (RFC 6901) из сегментов пути, например
// ptr("issues", "2", "opponent", "limit") -> "/issues/2/opponent/limit".
func ptr(segments ...string) string {
	if len(segments) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(escapePointerSegment(s))
	}
	return b.String()
}

// ptrChild добавляет один сегмент к уже построенному пути.
func ptrChild(base, segment string) string {
	return base + "/" + escapePointerSegment(segment)
}

// ptrIndex добавляет индекс элемента списка к уже построенному пути.
func ptrIndex(base string, idx int) string {
	return base + "/" + strconv.Itoa(idx)
}

func escapePointerSegment(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

package httpx

import "fmt"

// FindEmotionKey обходит уже разобранное тело (map[string]any / []any —
// как отдаёт encoding/json) и ищет ключ с именем ровно "emotion" на любой
// глубине (FR-RS-02, I-1). Совпадение точное, не подстрока: поле
// camera.emotion_labels_to_opponent обязано пройти. Возвращает JSON Pointer
// до найденного ключа для сообщения об ошибке.
func FindEmotionKey(v any) (pointer string, found bool) {
	return findEmotionKey(v, "")
}

func findEmotionKey(v any, path string) (string, bool) {
	switch node := v.(type) {
	case map[string]any:
		if _, ok := node["emotion"]; ok {
			return path + "/emotion", true
		}
		for key, child := range node {
			if p, ok := findEmotionKey(child, path+"/"+escapePointerSegment(key)); ok {
				return p, true
			}
		}
	case []any:
		for i, child := range node {
			if p, ok := findEmotionKey(child, fmt.Sprintf("%s/%d", path, i)); ok {
				return p, true
			}
		}
	}
	return "", false
}

func escapePointerSegment(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

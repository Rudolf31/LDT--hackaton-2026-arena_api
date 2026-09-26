package httpx

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, body string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return v
}

func TestFindEmotionKeyAtDepth(t *testing.T) {
	v := decode(t, `{"a":{"b":{"c":[{"emotion":"happy"}]}}}`)
	pointer, found := FindEmotionKey(v)
	if !found {
		t.Fatal("ключ emotion на глубине должен быть найден")
	}
	if pointer != "/a/b/c/0/emotion" {
		t.Fatalf("неверный указатель: %s", pointer)
	}
}

func TestFindEmotionKeyExactNameOnly(t *testing.T) {
	// camera.emotion_labels_to_opponent — законное имя (I-1): подстрока не считается.
	v := decode(t, `{"camera":{"emotion_labels_to_opponent":true}}`)
	if _, found := FindEmotionKey(v); found {
		t.Fatal("emotion_labels_to_opponent не должен считаться совпадением — только точное имя emotion")
	}
}

func TestFindEmotionKeyNotPresent(t *testing.T) {
	v := decode(t, `{"a":1,"b":[1,2,3],"c":{"d":"текст"}}`)
	if _, found := FindEmotionKey(v); found {
		t.Fatal("ключа emotion в теле нет — не должен находиться")
	}
}

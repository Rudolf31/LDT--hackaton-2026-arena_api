package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAllKindsHaveStatus — I-8: у каждой доменной ошибки есть код состояния
// и (проверяется отдельно в TestWriteErrorProducesRussianTitle) непустой
// русский title. Незнакомый Kind не должен молча отвечать 200.
func TestAllKindsHaveStatus(t *testing.T) {
	for kind := range statusByKind {
		status := (&Error{Kind: kind}).Status()
		if status < 400 {
			t.Fatalf("Kind %q должен отвечать кодом ошибки, получили %d", kind, status)
		}
	}
}

func TestWriteErrorProducesRussianTitle(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, NewError(KindForbiddenGroup, "Нет доступа к этой группе.").WithRetryAfter(7))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("ожидался 403, получили %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("неверный Content-Type: %s", ct)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "7" {
		t.Fatalf("неверный Retry-After: %s", ra)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("тело не разобралось: %v", err)
	}
	title, _ := body["title"].(string)
	if title == "" {
		t.Fatal("title не должен быть пустым")
	}
	for _, r := range title {
		if r > 127 {
			return // нашли не-ASCII символ — русский текст
		}
	}
	t.Fatalf("title не похож на русский текст: %q", title)
}

func TestNotImplementedIs501(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, NotImplemented())
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("ожидался 501, получили %d", rec.Code)
	}
}

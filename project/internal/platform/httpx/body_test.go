package httpx

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testSpecYAML = `
openapi: 3.1.0
info: {title: test, version: "0"}
paths:
  /test/echo:
    get:
      operationId: testEchoGet
    post:
      operationId: testEcho
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: false
              required: [name]
              properties:
                name: {type: string, minLength: 1}
`

func testBodyMiddleware(t *testing.T) func(http.Handler) http.Handler {
	t.Helper()
	schemas, err := LoadBodySchemas([]byte(testSpecYAML), 1024, nil)
	if err != nil {
		t.Fatalf("LoadBodySchemas: %v", err)
	}
	return Body(schemas)
}

func doPost(t *testing.T, mw func(http.Handler) http.Handler, contentType string, body []byte) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, body) {
			t.Fatalf("тело до обработчика изменилось: было %q, стало %q", body, got)
		}
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test/echo", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	mw(next).ServeHTTP(rec, req)
	return rec, called
}

func TestBodyRejectsEmotionKey(t *testing.T) {
	mw := testBodyMiddleware(t)
	rec, called := doPost(t, mw, "application/json", []byte(`{"name":"ok","camera":{"emotion":"happy"}}`))
	if called {
		t.Fatal("обработчик не должен был вызваться — ключ emotion")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получили %d", rec.Code)
	}
}

func TestBodyAllowsExactFieldNameContainingEmotionSubstring(t *testing.T) {
	// camera.emotion_labels_to_opponent — законное имя схемы не проверяем тут
	// (в тестовой схеме его нет), но сама функция сканирования уже проверена
	// в emotion_test.go; здесь достаточно валидного тела без emotion.
	mw := testBodyMiddleware(t)
	body := []byte(`{"name":"ок"}`)
	rec, called := doPost(t, mw, "application/json", body)
	if !called {
		t.Fatalf("обработчик должен был вызваться, ответ: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получили %d", rec.Code)
	}
}

func TestBodyRejectsNonJSONContentType(t *testing.T) {
	mw := testBodyMiddleware(t)
	rec, called := doPost(t, mw, "text/plain", []byte(`{"name":"ок"}`))
	if called {
		t.Fatal("обработчик не должен был вызваться — не JSON")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получили %d", rec.Code)
	}
}

func TestBodyRejectsTooLarge(t *testing.T) {
	mw := testBodyMiddleware(t)
	big := bytes.Repeat([]byte("a"), 2048)
	body := []byte(`{"name":"` + string(big) + `"}`)
	rec, called := doPost(t, mw, "application/json", body)
	if called {
		t.Fatal("обработчик не должен был вызваться — тело больше лимита")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ожидался 413, получили %d", rec.Code)
	}
}

func TestBodyRejectsSchemaViolation(t *testing.T) {
	mw := testBodyMiddleware(t)
	rec, called := doPost(t, mw, "application/json", []byte(`{"name":"ок","extra":1}`))
	if called {
		t.Fatal("обработчик не должен был вызваться — лишнее поле")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получили %d", rec.Code)
	}
	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("тело не JSON: %v", err)
	}
	errs, ok := problem["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("ожидались errors[] с описанием поля: %v", problem)
	}
}

func TestBodyIgnoresStrayContentTypeWithoutBody(t *testing.T) {
	mw := testBodyMiddleware(t)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Клиенты нередко ставят Content-Type на каждый запрос по умолчанию,
	// включая GET без тела — сам по себе этот заголовок не должен приводить
	// к попытке разобрать пустое тело как JSON.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test/echo", nil)
	req.Header.Set("Content-Type", "application/json")
	mw(next).ServeHTTP(rec, req)

	if !called {
		t.Fatalf("обработчик должен был вызваться — тела нет, только заголовок Content-Type; ответ: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получили %d", rec.Code)
	}
}

func TestBodyRequiredMissing(t *testing.T) {
	mw := testBodyMiddleware(t)
	rec, called := doPost(t, mw, "", nil)
	if called {
		t.Fatal("обработчик не должен был вызваться — нет тела")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получили %d", rec.Code)
	}
}

func TestBodyPutsCheckedRawBodyIntoContext(t *testing.T) {
	var got []byte
	var ok bool
	handler := testBodyMiddleware(t)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = RawBody(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	body := `{"name":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/test/echo", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("статус %d, тело %s", rec.Code, rec.Body.String())
	}
	if !ok || string(got) != body {
		t.Fatalf("RawBody: ok=%v, тело %q", ok, got)
	}
}

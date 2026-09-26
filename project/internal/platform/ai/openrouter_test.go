package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGenerateSendsJSONObjectFormat — форма запроса /chat/completions:
// модель, response_format.type=json_object, оба сообщения (D-32/D-33).
func TestGenerateSendsJSONObjectFormat(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("неожиданный путь: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("тело не разобралось: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"format":"arena-scenario/1"}`}},
			},
		})
	}))
	defer server.Close()

	gen := NewGenerator(server.URL, "test-key", "test-model", "системный промпт")
	doc, err := gen.Generate(context.Background(), "опиши сценарий", nil, nil)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !json.Valid(doc) {
		t.Fatalf("документ не JSON: %s", doc)
	}

	if gotBody["model"] != "test-model" {
		t.Fatalf("модель не передана: %v", gotBody["model"])
	}
	rf, _ := gotBody["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("response_format.type должен быть json_object: %v", gotBody["response_format"])
	}
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("ожидалось два сообщения (система + пользователь), получено %d", len(messages))
	}
	sys, _ := messages[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "системный промпт" {
		t.Fatalf("первое сообщение должно быть системным промптом: %v", sys)
	}
	user, _ := messages[1].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("второе сообщение должно быть от пользователя: %v", user)
	}
}

// TestGenerateSendsBaseAndProblems — правка: текущий документ и
// диагностика прошлой попытки уходят в тексте сообщения пользователя.
func TestGenerateSendsBaseAndProblems(t *testing.T) {
	var userContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "user" {
				userContent = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{}`}}},
		})
	}))
	defer server.Close()

	gen := NewGenerator(server.URL, "key", "model", "промпт")
	base := []byte(`{"passport":{"title":"старый"}}`)
	_, err := gen.Generate(context.Background(), "смени название на «Новое»",
		base, []string{"/passport/title: пусто"})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !strings.Contains(userContent, "смени название") {
		t.Fatalf("указание не ушло модели: %q", userContent)
	}
	if !strings.Contains(userContent, `"старый"`) {
		t.Fatalf("текущий документ не ушёл модели: %q", userContent)
	}
	if !strings.Contains(userContent, "/passport/title: пусто") {
		t.Fatalf("диагностика прошлой попытки не ушла модели: %q", userContent)
	}
}

// TestGenerateStripsJSONFence — модель обернула ответ в ```json несмотря
// на response_format=json_object; клиент всё равно должен вернуть чистый JSON.
func TestGenerateStripsJSONFence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "```json\n{\"a\":1}\n```"}}},
		})
	}))
	defer server.Close()

	gen := NewGenerator(server.URL, "key", "model", "промпт")
	doc, err := gen.Generate(context.Background(), "brief", nil, nil)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if string(doc) != `{"a":1}` {
		t.Fatalf("ограда ```json не снята: %s", doc)
	}
}

// TestGenerateNotJSONIsUpstreamError — модель вернула не JSON.
func TestGenerateNotJSONIsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "не json вовсе"}}},
		})
	}))
	defer server.Close()

	gen := NewGenerator(server.URL, "key", "model", "промпт")
	_, err := gen.Generate(context.Background(), "brief", nil, nil)
	if err == nil {
		t.Fatal("ожидалась ошибка на не-JSON ответе")
	}
}

// TestGenerateUpstream5xx — сбой провайдера классифицируется как
// ErrUpstream, без текста тела ответа в самой ошибке.
func TestGenerateUpstream5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "секретная подробность провайдера"},
		})
	}))
	defer server.Close()

	gen := NewGenerator(server.URL, "key", "model", "промпт")
	_, err := gen.Generate(context.Background(), "brief", nil, nil)
	if err == nil {
		t.Fatal("ожидалась ошибка на 5xx")
	}
	if strings.Contains(err.Error(), "секретная подробность") {
		t.Fatalf("тело ответа провайдера не должно попадать в ошибку: %v", err)
	}
}

// TestGenerateTimeout — контекст истёк раньше ответа → ErrTimeout.
func TestGenerateTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{}`}}},
		})
	}))
	defer server.Close()

	gen := NewGenerator(server.URL, "key", "model", "промпт")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := gen.Generate(ctx, "brief", nil, nil)
	if err == nil {
		t.Fatal("ожидалась ошибка по таймауту")
	}
}

// TestTranscribeSendsMp3Format — форма запроса /audio/transcriptions:
// input_audio.format=mp3, данные в base64, язык ru (D-32).
func TestTranscribeSendsMp3Format(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Fatalf("неожиданный путь: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("тело не разобралось: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "надиктованное описание"})
	}))
	defer server.Close()

	tr := NewTranscriber(server.URL, "key", "stt-model")
	text, err := tr.Transcribe(context.Background(), strings.NewReader("fake-mp3-bytes"))
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if text != "надиктованное описание" {
		t.Fatalf("расшифровка не дошла: %q", text)
	}
	if gotBody["model"] != "stt-model" {
		t.Fatalf("модель не передана: %v", gotBody["model"])
	}
	if gotBody["language"] != "ru" {
		t.Fatalf("язык должен быть ru: %v", gotBody["language"])
	}
	inputAudio, _ := gotBody["input_audio"].(map[string]any)
	if inputAudio["format"] != "mp3" {
		t.Fatalf("формат должен быть mp3: %v", inputAudio["format"])
	}
	if _, ok := inputAudio["data"].(string); !ok {
		t.Fatalf("данные должны быть строкой (base64): %v", inputAudio["data"])
	}
}

// TestTranscribeUpstreamError — 5xx при расшифровке тоже классифицируется,
// без утечки тела ответа.
func TestTranscribeUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "перегрузка"}})
	}))
	defer server.Close()

	tr := NewTranscriber(server.URL, "key", "model")
	_, err := tr.Transcribe(context.Background(), strings.NewReader("mp3"))
	if err == nil {
		t.Fatal("ожидалась ошибка на 5xx")
	}
	if strings.Contains(err.Error(), "перегрузка") {
		t.Fatalf("тело ответа провайдера не должно попадать в ошибку: %v", err)
	}
}

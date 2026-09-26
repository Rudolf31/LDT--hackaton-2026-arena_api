package generation

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"arena-portal-backend/internal/platform/httpx"
)

func multipartRequest(t *testing.T, fieldName string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(fieldName, "запись.mp3")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestReadAudioPartOK(t *testing.T) {
	req := multipartRequest(t, "file", []byte("fake-mp3-bytes"))
	rec := httptest.NewRecorder()
	data, err := readAudioPart(rec, req)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if string(data) != "fake-mp3-bytes" {
		t.Fatalf("неверные данные: %q", data)
	}
}

func TestReadAudioPartTooLarge(t *testing.T) {
	req := multipartRequest(t, "file", bytes.Repeat([]byte{0}, maxAudioBytes+1))
	rec := httptest.NewRecorder()
	_, err := readAudioPart(rec, req)
	var httpErr *httpx.Error
	if err == nil {
		t.Fatal("ожидалась ошибка на файле больше 25 МБ")
	}
	if httpErrAs, ok := err.(*httpx.Error); ok {
		httpErr = httpErrAs
	}
	if httpErr == nil || httpErr.Kind != httpx.KindTooLarge {
		t.Fatalf("ожидался too_large (413), получено %v", err)
	}
}

func TestReadAudioPartEmpty(t *testing.T) {
	req := multipartRequest(t, "file", nil)
	rec := httptest.NewRecorder()
	_, err := readAudioPart(rec, req)
	if err == nil {
		t.Fatal("ожидалась ошибка на пустом файле")
	}
}

func TestReadAudioPartWrongFieldName(t *testing.T) {
	req := multipartRequest(t, "audio", []byte("data"))
	rec := httptest.NewRecorder()
	_, err := readAudioPart(rec, req)
	if err == nil {
		t.Fatal("ожидалась ошибка при неверном имени поля")
	}
}

func TestReadAudioPartNotMultipart(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("не multipart"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	_, err := readAudioPart(rec, req)
	if err == nil {
		t.Fatal("ожидалась ошибка на не-multipart запросе")
	}
}

func jsonRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	return req
}

func TestReadTextFieldOK(t *testing.T) {
	rec := httptest.NewRecorder()
	text, err := readTextField(rec, jsonRequest(`{"text":"описание сценария"}`), "text", 1, 20000)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if text != "описание сценария" {
		t.Fatalf("неверный текст: %q", text)
	}
}

// TestReadTextFieldEmotionRejected — I-1: ключ emotion на любой глубине
// тела → 400, до записи тела куда-либо.
func TestReadTextFieldEmotionRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	_, err := readTextField(rec, jsonRequest(`{"text":"описание","meta":{"emotion":"радость"}}`), "text", 1, 20000)
	httpErr, ok := err.(*httpx.Error)
	if !ok || httpErr.Kind != httpx.KindInvalidBody {
		t.Fatalf("ожидался invalid_body на ключе emotion, получено %v", err)
	}
}

func TestReadTextFieldTooShort(t *testing.T) {
	rec := httptest.NewRecorder()
	_, err := readTextField(rec, jsonRequest(`{"instruction":""}`), "instruction", 1, 4000)
	if err == nil {
		t.Fatal("ожидалась ошибка на пустом instruction")
	}
}

func TestReadTextFieldTooLong(t *testing.T) {
	long := strings.Repeat("а", 4001)
	rec := httptest.NewRecorder()
	_, err := readTextField(rec, jsonRequest(`{"instruction":"`+long+`"}`), "instruction", 1, 4000)
	if err == nil {
		t.Fatal("ожидалась ошибка на слишком длинном instruction")
	}
}

func TestReadTextFieldUnknownField(t *testing.T) {
	rec := httptest.NewRecorder()
	_, err := readTextField(rec, jsonRequest(`{"text":"описание","extra":1}`), "text", 1, 20000)
	if err == nil {
		t.Fatal("ожидалась ошибка на лишнем поле — схема закрытая")
	}
}

func TestReadTextFieldWrongContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"text":"a"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	_, err := readTextField(rec, req, "text", 1, 20000)
	if err == nil {
		t.Fatal("ожидалась ошибка на не-JSON Content-Type")
	}
}

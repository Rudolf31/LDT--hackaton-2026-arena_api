package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

const secretErrText = "чтение настроек портала: pgx: внутренний сбой драйвера на строке 42"

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestHandleRequestErrorDoesNotLeakRawErrorToClient(t *testing.T) {
	logger, logBuf := newTestLogger()
	handler := HandleRequestError(logger)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/portal/settings", nil)
	handler(rec, req, errors.New(secretErrText))

	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("тело не JSON: %v", err)
	}
	if detail, _ := problem["detail"].(string); strings.Contains(detail, secretErrText) {
		t.Fatalf("текст внутренней ошибки утёк клиенту в detail: %v", problem)
	}
	if !strings.Contains(logBuf.String(), secretErrText) {
		t.Fatalf("текст ошибки должен был попасть в лог, лог: %s", logBuf.String())
	}
}

func TestHandleResponseErrorDoesNotLeakRawErrorToClient(t *testing.T) {
	logger, logBuf := newTestLogger()
	handler := HandleResponseError(logger)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/portal/settings", nil)
	handler(rec, req, errors.New(secretErrText))

	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("тело не JSON: %v", err)
	}
	if detail, _ := problem["detail"].(string); strings.Contains(detail, secretErrText) {
		t.Fatalf("текст внутренней ошибки утёк клиенту в detail: %v", problem)
	}
	if !strings.Contains(logBuf.String(), secretErrText) {
		t.Fatalf("текст ошибки должен был попасть в лог, лог: %s", logBuf.String())
	}
}

func TestHandleResponseErrorPassesThroughDomainError(t *testing.T) {
	logger, _ := newTestLogger()
	handler := HandleResponseError(logger)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/portal/settings", nil)
	handler(rec, req, NewError(KindNotFound, "Такого адреса нет."))

	if rec.Code != 404 {
		t.Fatalf("доменная ошибка должна была пройти как есть, получили %d: %s", rec.Code, rec.Body.String())
	}
}

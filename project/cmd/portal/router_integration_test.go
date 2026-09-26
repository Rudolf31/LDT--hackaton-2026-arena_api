//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/platform/config"
	"arena-portal-backend/internal/platform/pgtest"
)

// syncBuffer — перехваченный лог портала: I-5 ищет в нём имена.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	log    *syncBuffer
}

// newTestEnv собирает портал тем же buildApp, что и main, на чистой базе;
// заливка (D-23) заводит admin и methodologist и демо-группу.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pool := pgtest.NewDatabase(t)
	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	router, err := buildApp(context.Background(), pool, testConfig(), logger)
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}
	return &testEnv{t: t, pool: pool, router: router, log: logBuf}
}

func testConfig() config.Config {
	return config.Config{
		MasterKey:      bytes.Repeat([]byte{1}, 32),
		CodeHMACSecret: bytes.Repeat([]byte{2}, 32),
		Demo:           true,
		CookieSecure:   true,
		TrainerURL:     "http://trainer.test",
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

const (
	adminLogin    = "admin"
	adminPassword = "arena-admin-2026"
	methLogin     = "methodologist"
	methPassword  = "arena-method-2026"
)

func (e *testEnv) do(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) login(login, password string) *http.Cookie {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/portal/auth/login", `{"login":"`+login+`","password":"`+password+`"}`, nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("вход %s: ожидался 200, получили %d: %s", login, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "arena_session" && c.Value != "" {
			return c
		}
	}
	e.t.Fatalf("вход %s: нет cookie arena_session", login)
	return nil
}

func (e *testEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("тело не JSON: %v: %s", err, rec.Body.String())
	}
	return v
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("ожидался %d, получили %d: %s", want, rec.Code, rec.Body.String())
	}
}

func TestRouterUnimplementedOperationAnswers501(t *testing.T) {
	e := newTestEnv(t)
	// /assignments живёт с этапа 07; список сессий остаётся 501 до этапа 09.
	rec := e.do(http.MethodGet, "/api/portal/sessions", "", e.login(adminLogin, adminPassword))
	expectStatus(t, rec, http.StatusNotImplemented)
	if problem := decode[map[string]any](t, rec); problem["title"] == "" {
		t.Fatal("title не должен быть пустым")
	}
}

func TestRouterSettingsRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(adminLogin, adminPassword)

	expectStatus(t, e.do(http.MethodGet, "/api/portal/settings", "", admin), http.StatusOK)

	rec := e.do(http.MethodPatch, "/api/portal/settings", `{"admission_rehearsals":5}`, admin)
	expectStatus(t, rec, http.StatusOK)
	body := decode[map[string]any](t, rec)
	if body["admission_rehearsals"] != float64(5) || body["updated_by_name"] != "Администратор демо" {
		t.Fatalf("настройки не сохранились или без автора: %v", body)
	}
}

func TestRouterRejectsEmotionKeyBeforeReachingHandler(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(http.MethodPatch, "/api/portal/settings", `{"admission_rehearsals":5,"camera":{"emotion":"happy"}}`, e.login(adminLogin, adminPassword))
	expectStatus(t, rec, http.StatusBadRequest)
}

func TestRouterUnknownPathAnswersProblemJSON(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(http.MethodGet, "/nope", "", nil)
	expectStatus(t, rec, http.StatusNotFound)
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("неверный Content-Type: %s", ct)
	}
}

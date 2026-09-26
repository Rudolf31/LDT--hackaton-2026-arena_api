//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"arena-portal-backend/internal/modules/scenariodoc"
	"arena-portal-backend/internal/platform/pgtest"
)

// --- помощники ---

// newFakeOpenRouter — двойник OpenRouter для этапа 05: chatContent уходит
// как есть в choices[0].message.content (уже готовый JSON-текст документа
// сценария), sttText — как text ответа /audio/transcriptions. delay
// придерживает ответ /chat/completions — нужен тестам, которым важно
// застать задание в состоянии running.
func newFakeOpenRouter(t *testing.T, delay time.Duration, chatContent, sttText string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": chatContent}}},
		})
	})
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"text": sttText})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func salesTemplateJSON(t *testing.T) string {
	t.Helper()
	for _, tpl := range scenariodoc.Templates() {
		if tpl.ID == "sales" {
			return string(tpl.Document)
		}
	}
	t.Fatal("шаблон sales не найден")
	return ""
}

// newTestEnvWithModels — тот же портал, что newTestEnv, но с настроенными
// моделями авторства (D-34/D-35): generation.New получает настоящие
// ai.NewGenerator/ai.NewTranscriber, указывающие на httptest-сервер вместо
// живого OpenRouter (ключ и адрес которого пользователь намеренно не дал).
func newTestEnvWithModels(t *testing.T, genURL, sttURL string) *testEnv {
	t.Helper()
	pool := pgtest.NewDatabase(t)
	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := testConfig()
	cfg.GenURL, cfg.GenKey, cfg.GenModel = genURL, "test-gen-key", "test-gen-model"
	if sttURL != "" {
		cfg.STTURL, cfg.STTKey, cfg.STTModel = sttURL, "test-stt-key", "test-stt-model"
	}
	router, err := buildApp(context.Background(), pool, cfg, logger)
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}
	return &testEnv{t: t, pool: pool, router: router, log: logBuf}
}

// pollGeneration опрашивает GET /generation, пока status = running
// (arena-portal-hr.md 11.6: экран делает то же самое раз в две секунды).
func (e *testEnv) pollGeneration(cookie *http.Cookie, id string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := e.do(http.MethodGet, "/api/portal/scenarios/"+id+"/generation", "", cookie)
		expectStatus(e.t, rec, http.StatusOK)
		body := decode[map[string]any](e.t, rec)
		if body["status"] != "running" {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("генерация не завершилась вовремя")
	return nil
}

func (e *testEnv) doMultipart(path, fieldName string, content []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(fieldName, "запись.mp3")
	if err != nil {
		e.t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		e.t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		e.t.Fatalf("Close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// --- без моделей: 4 адреса → 503, форма и шаблоны работают ---

func TestGenerationWithoutModelsIs503(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"Без моделей"}`)
	id := card["id"].(string)

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"описание"}`, meth)
	expectStatus(t, rec, http.StatusServiceUnavailable)
	if p := decode[map[string]any](t, rec); p["type"] != "generation_unavailable" || p["title"] != "Модель генерации не настроена." {
		t.Fatalf("неверный problem+json: %v", p)
	}

	rec = e.doMultipart("/api/portal/scenarios/"+id+"/generation/audio", "file", []byte("mp3"), meth)
	expectStatus(t, rec, http.StatusServiceUnavailable)

	// форма, шаблоны и остальной портал по сценариям работают как обычно.
	rec = e.do(http.MethodGet, "/api/portal/scenario-templates", "", meth)
	expectStatus(t, rec, http.StatusOK)
	rec = e.do(http.MethodGet, "/api/portal/scenarios/"+id, "", meth)
	expectStatus(t, rec, http.StatusOK)
}

// --- I-16: GetGeneration больше не 501 ---

func TestGetGenerationIsNotUnimplemented(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	rec := e.do(http.MethodGet, "/api/portal/scenarios/"+card["id"].(string)+"/generation", "", meth)
	expectStatus(t, rec, http.StatusOK)
	body := decode[map[string]any](t, rec)
	if body["status"] != "idle" {
		t.Fatalf("сценарий без генерации должен быть idle: %v", body)
	}
}

// --- happy path: текст → документ, passport приведён к сценарию ---

func TestGenerationTextHappyPath(t *testing.T) {
	genServer := newFakeOpenRouter(t, 0, salesTemplateJSON(t), "")
	e := newTestEnvWithModels(t, genServer.URL, "")
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"brief","mode":"assessment","title":"Через генерацию"}`)
	id := card["id"].(string)

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"опиши продажу услуги клиенту"}`, meth)
	expectStatus(t, rec, http.StatusAccepted)
	started := decode[map[string]any](t, rec)
	if started["status"] != "running" {
		t.Fatalf("ожидался running сразу после запуска: %v", started)
	}

	final := e.pollGeneration(meth, id)
	if final["status"] != "done" {
		t.Fatalf("ожидался done: %v", final)
	}

	d, _ := e.draft(meth, id)
	doc := d["document"].(map[string]any)
	passport := doc["passport"].(map[string]any)
	if passport["mode"] != "assessment" {
		t.Fatalf("passport.mode должен быть приведён к режиму сценария: %v", passport["mode"])
	}
	if passport["title"] != "Через генерацию" {
		t.Fatalf("passport.title должен быть названием сценария: %v", passport["title"])
	}
	authoring := doc["authoring"].(map[string]any)
	if authoring["brief"] != "опиши продажу услуги клиенту" {
		t.Fatalf("authoring.brief должен быть присланным текстом: %v", authoring["brief"])
	}
}

// --- второй запуск во время running → 409; PUT /draft во время running → 409 ---

func TestGenerationRunningBlocksSecondStartAndDraftEdit(t *testing.T) {
	genServer := newFakeOpenRouter(t, 400*time.Millisecond, salesTemplateJSON(t), "")
	e := newTestEnvWithModels(t, genServer.URL, "")
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"manual","mode":"training"}`)
	id := card["id"].(string)
	d, _ := e.draft(meth, id)
	doc := d["document"].(map[string]any)
	draftBody, err := json.Marshal(map[string]any{"document": doc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"описание"}`, meth)
	expectStatus(t, rec, http.StatusAccepted)

	// второй запуск, пока первый ещё точно идёт (сервер держит ответ 400 мс)
	rec2 := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"другое описание"}`, meth)
	expectStatus(t, rec2, http.StatusConflict)
	if p := decode[map[string]any](t, rec2); p["type"] != "generation_running" {
		t.Fatalf("ожидался generation_running: %v", p)
	}

	// правка черновика во время генерации — тоже 409
	rec3 := e.do(http.MethodPut, "/api/portal/scenarios/"+id+"/draft", string(draftBody), meth)
	expectStatus(t, rec3, http.StatusConflict)
	if p := decode[map[string]any](t, rec3); p["type"] != "generation_running" {
		t.Fatalf("ожидался generation_running на правке черновика: %v", p)
	}

	final := e.pollGeneration(meth, id)
	if final["status"] != "done" {
		t.Fatalf("ожидался done по завершении: %v", final)
	}
}

// --- voice-edit/text-edit без черновика → 409 ---

func TestDraftEditWithoutDraftIs409(t *testing.T) {
	genServer := newFakeOpenRouter(t, 0, salesTemplateJSON(t), "")
	e := newTestEnvWithModels(t, genServer.URL, "")
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"Без черновика"}`)
	id := card["id"].(string)

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/draft/text-edit", `{"instruction":"смени название"}`, meth)
	expectStatus(t, rec, http.StatusConflict)
	if p := decode[map[string]any](t, rec); p["type"] != "draft_missing" {
		t.Fatalf("ожидался draft_missing: %v", p)
	}
}

// --- 11-й запрос за час от одного пользователя → 429 ---

func TestGenerationRateLimited(t *testing.T) {
	e := newTestEnv(t) // без моделей — 503 на каждой попытке, но частота считается до этого
	meth := e.login(methLogin, methPassword)
	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"Частота"}`)
	id := card["id"].(string)

	for i := 0; i < 10; i++ {
		rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"описание"}`, meth)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("предел не должен сработать раньше 11-го запроса (попытка %d)", i+1)
		}
	}
	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"описание"}`, meth)
	expectStatus(t, rec, http.StatusTooManyRequests)
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Fatal("ожидался заголовок Retry-After")
	}
	if p := decode[map[string]any](t, rec); p["type"] != "rate_limited" {
		t.Fatalf("ожидался rate_limited: %v", p)
	}
}

// --- наблюдатель → 403 ---

func TestGenerationDeniedForObserver(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	observer := e.createObserver()
	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"Наблюдатель"}`)
	id := card["id"].(string)

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text", `{"text":"описание"}`, observer)
	expectStatus(t, rec, http.StatusForbidden)
	if p := decode[map[string]any](t, rec); p["type"] != "forbidden_role" {
		t.Fatalf("ожидался forbidden_role: %v", p)
	}
}

// --- рестарт: running → failed с русским текстом ---

func TestGenerationRestartFailsRunningJobs(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"На перезапуск"}`)
	id := card["id"].(string)

	if _, err := e.pool.Exec(context.Background(),
		`UPDATE scenarios SET generation = generation || '{"status":"running"}'::jsonb WHERE id = $1`, id,
	); err != nil {
		t.Fatalf("exec: %v", err)
	}

	// «перезапуск портала» — вторая сборка на том же пуле; Recover входит в buildApp.
	logBuf2 := &syncBuffer{}
	logger2 := slog.New(slog.NewJSONHandler(logBuf2, &slog.HandlerOptions{Level: slog.LevelDebug}))
	router2, err := buildApp(context.Background(), e.pool, testConfig(), logger2)
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}
	e2 := &testEnv{t: t, pool: e.pool, router: router2, log: logBuf2}
	meth2 := e2.login(methLogin, methPassword)

	rec := e2.do(http.MethodGet, "/api/portal/scenarios/"+id+"/generation", "", meth2)
	expectStatus(t, rec, http.StatusOK)
	body := decode[map[string]any](t, rec)
	if body["status"] != "failed" {
		t.Fatalf("ожидался failed после перезапуска: %v", body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["message"] != "Портал перезапускался — повторите." {
		t.Fatalf("неверное сообщение: %v", errObj)
	}
}

// --- emotion в теле generation/text → 400, тело не в логе ---

func TestGenerationTextRejectsEmotionKey(t *testing.T) {
	e := newTestEnv(t)
	meth := e.login(methLogin, methPassword)
	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"emotion"}`)
	id := card["id"].(string)

	rec := e.do(http.MethodPost, "/api/portal/scenarios/"+id+"/generation/text",
		`{"text":"описание","emotion":"радость"}`, meth)
	expectStatus(t, rec, http.StatusBadRequest)

	if strings.Contains(e.log.String(), "радость") {
		t.Fatal("тело запроса с ключом emotion не должно попасть в лог")
	}
}

// --- I-11: mp3 не сохраняется нигде, расшифровка не попадает в лог и в audit_log ---

func TestGenerationVoiceDoesNotLeakAudioOrTranscript(t *testing.T) {
	const transcriptMarker = "МаркерРасшифровки-7f3c1a"
	const audioMarker = "МаркерMP3Байтов-9d2e4b"

	genServer := newFakeOpenRouter(t, 0, salesTemplateJSON(t), transcriptMarker+": надиктованное описание сценария")
	sttServer := genServer // один и тот же двойник отвечает на оба пути
	e := newTestEnvWithModels(t, genServer.URL, sttServer.URL)
	meth := e.login(methLogin, methPassword)

	card := e.createScenario(meth, `{"origin":"brief","mode":"training","title":"Голосом"}`)
	id := card["id"].(string)

	rec := e.doMultipart("/api/portal/scenarios/"+id+"/generation/audio", "file", []byte(audioMarker+audioMarker), meth)
	expectStatus(t, rec, http.StatusAccepted)

	final := e.pollGeneration(meth, id)
	if final["status"] != "done" {
		t.Fatalf("ожидался done: %v", final)
	}
	if !strings.Contains(final["transcript"].(string), transcriptMarker) {
		t.Fatalf("расшифровка должна попасть в scenarios.generation (GET /generation): %v", final)
	}

	// расшифровка — не имя и не реплика участника (это надиктованное
	// методологом описание сценария), но I-11 запрещает её лог и audit_log
	// так же, как и сам звук.
	if strings.Contains(e.log.String(), transcriptMarker) {
		t.Fatal("расшифровка не должна попасть в лог")
	}
	if strings.Contains(e.log.String(), audioMarker) {
		t.Fatal("mp3 не должен попасть в лог")
	}

	var auditCount int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE details::text ILIKE '%'||$1||'%'`, transcriptMarker,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if auditCount != 0 {
		t.Fatal("расшифровка не должна попасть в audit_log.details")
	}
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE details::text ILIKE '%'||$1||'%'`, audioMarker,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if auditCount != 0 {
		t.Fatal("mp3 не должен попасть в audit_log.details")
	}

	// mp3 нигде не хранится — ни в generation, ни в draft_document.
	var scenarioRowCount int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scenarios WHERE id = $1 AND (generation::text ILIKE '%'||$2||'%' OR draft_document::text ILIKE '%'||$2||'%')`,
		id, audioMarker,
	).Scan(&scenarioRowCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if scenarioRowCount != 0 {
		t.Fatal("mp3 не должен попасть в scenarios.generation/draft_document")
	}
}

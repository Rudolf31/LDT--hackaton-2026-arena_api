package generation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/scenariodoc"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/ai"
	"arena-portal-backend/internal/platform/httpx"
)

// fakeAuthoring — двойник scenarios.Authoring для тестов сервиса
// generation (один сценарий на экземпляр — этого достаточно для проверки
// правил самого задания; правила BeginGeneration/SaveDraft уже проверены
// в internal/modules/scenarios).
type fakeAuthoring struct {
	mu sync.Mutex

	state    json.RawMessage
	draft    json.RawMessage
	mode     gen.Mode
	archived bool

	beginErr     error
	failMessages []string
}

func newFakeAuthoring(mode gen.Mode) *fakeAuthoring {
	return &fakeAuthoring{mode: mode}
}

func (f *fakeAuthoring) GenerationState(context.Context, uuid.UUID) (scenarios.GenerationSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return scenarios.GenerationSnapshot{State: f.state, Draft: f.draft, Mode: f.mode, Archived: f.archived}, nil
}

func (f *fakeAuthoring) BeginGeneration(_ context.Context, _ uuid.UUID, _ bool, state json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beginErr != nil {
		return f.beginErr
	}
	f.state = state
	return nil
}

func (f *fakeAuthoring) UpdateGeneration(_ context.Context, _ uuid.UUID, state json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
	return nil
}

func (f *fakeAuthoring) FinishGeneration(_ context.Context, _ uuid.UUID, state json.RawMessage, document json.RawMessage, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
	if document != nil {
		f.draft = document
	}
	return nil
}

func (f *fakeAuthoring) FailRunningGenerations(_ context.Context, message string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failMessages = append(f.failMessages, message)
	return 1, nil
}

func (f *fakeAuthoring) snapshotState(t *testing.T) jobState {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var st jobState
	if f.state == nil {
		t.Fatal("generation ничего не записал")
	}
	if err := json.Unmarshal(f.state, &st); err != nil {
		t.Fatalf("состояние не разобралось: %v", err)
	}
	return st
}

func (f *fakeAuthoring) draftDoc() json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.draft
}

var _ scenarios.Authoring = (*fakeAuthoring)(nil)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func salesTemplate(t *testing.T) []byte {
	t.Helper()
	for _, tpl := range scenariodoc.Templates() {
		if tpl.ID == "sales" {
			return tpl.Document
		}
	}
	t.Fatal("шаблон sales не найден")
	return nil
}

// --- Start: 503 без нужной модели (D-34) ---

func TestStartGenUnavailable(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	svc := newService(fa, nil, nil, testLogger(), context.Background())

	_, err := svc.Start(context.Background(), actor.Actor{UserID: uuid.New()}, uuid.New(), kindText, "описание", nil)
	var httpErr *httpx.Error
	if !errors.As(err, &httpErr) || httpErr.Kind != httpx.KindGenerationUnavailable {
		t.Fatalf("ожидалась generation_unavailable, получено %v", err)
	}
	if httpErr.Title != "Модель генерации не настроена." {
		t.Fatalf("неверный текст: %q", httpErr.Title)
	}
}

func TestStartSTTUnavailableForVoiceOnly(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{{Document: salesTemplate(t)}}}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	_, err := svc.Start(context.Background(), actor.Actor{UserID: uuid.New()}, uuid.New(), kindAudio, "", []byte("mp3"))
	var httpErr *httpx.Error
	if !errors.As(err, &httpErr) || httpErr.Kind != httpx.KindGenerationUnavailable {
		t.Fatalf("ожидалась generation_unavailable на голосовом вводе без расшифровки, получено %v", err)
	}
	if httpErr.Title != "Расшифровка речи не настроена." {
		t.Fatalf("неверный текст: %q", httpErr.Title)
	}

	// Текстовый ввод расшифровки не требует — должен запуститься.
	body, err := svc.Start(context.Background(), actor.Actor{UserID: uuid.New()}, uuid.New(), kindText, "описание", nil)
	if err != nil {
		t.Fatalf("текстовый ввод не должен требовать расшифровку: %v", err)
	}
	if body.Status != "running" {
		t.Fatalf("ожидался status=running сразу после запуска, получено %q", body.Status)
	}
}

// --- BeginGeneration отказ пробрасывается как есть (409/404 из scenarios) ---

func TestStartPropagatesBeginGenerationError(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	fa.beginErr = httpx.NewError(httpx.KindGenerationRunning, "Генерация уже идёт.")
	gener := &ai.FakeGenerator{}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	_, err := svc.Start(context.Background(), actor.Actor{UserID: uuid.New()}, uuid.New(), kindText, "текст", nil)
	var httpErr *httpx.Error
	if !errors.As(err, &httpErr) || httpErr.Kind != httpx.KindGenerationRunning {
		t.Fatalf("ошибка BeginGeneration должна дойти как есть, получено %v", err)
	}
}

// --- run(): полный цикл задания (вызывается напрямую — синхронно, без
// горутины Start, чтобы тест не гонялся за таймингом) ---

func TestRunTextSuccessDone(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeAssessment)
	tpl := salesTemplate(t)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{{Document: tpl}}}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	svc.run(uuid.New(), uuid.New(), kindText, "Моё название", "опиши сценарий продажи", nil, gen.ModeAssessment, nil)

	st := fa.snapshotState(t)
	if st.Status != "done" {
		t.Fatalf("ожидался status=done, получено %q (ошибка: %+v)", st.Status, st.Error)
	}
	if st.Check == nil {
		t.Fatal("check должен быть заполнен")
	}
	if gener.Calls != 1 {
		t.Fatalf("ожидался один вызов генератора для валидного ответа, получено %d", gener.Calls)
	}
	if fa.draftDoc() == nil {
		t.Fatal("документ должен быть сохранён в черновик")
	}

	// passport.mode принудительно приведён к режиму сценария, title и
	// authoring.brief — к названию и описанию («решения по умолчанию»
	// плана этапа 05).
	var doc struct {
		Passport struct {
			Mode  string `json:"mode"`
			Title string `json:"title"`
		} `json:"passport"`
		Authoring struct {
			Brief string `json:"brief"`
		} `json:"authoring"`
	}
	if err := json.Unmarshal(fa.draftDoc(), &doc); err != nil {
		t.Fatalf("документ не разобрался: %v", err)
	}
	if doc.Passport.Mode != "assessment" {
		t.Fatalf("passport.mode должен быть приведён к режиму сценария, получено %q", doc.Passport.Mode)
	}
	if doc.Passport.Title != "Моё название" {
		t.Fatalf("passport.title должен быть названием сценария, получено %q", doc.Passport.Title)
	}
	if doc.Authoring.Brief != "опиши сценарий продажи" {
		t.Fatalf("authoring.brief должен быть исходным описанием, получено %q", doc.Authoring.Brief)
	}
}

// TestRunRetriesOnBlockingErrors — 05-generation.md: неверный документ
// уходит модели на повтор (не больше двух), диагностика прошлой попытки —
// текстом. Здесь модель ни разу не исправляется — последняя попытка всё
// равно сохраняется как done, с диагностикой для правки в форме.
func TestRunRetriesOnBlockingErrors(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	invalid := []byte(`{"format":"arena-scenario/1","passport":{"id":"x","mode":"training"}}`)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{
		{Document: invalid}, {Document: invalid}, {Document: invalid},
	}}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	svc.run(uuid.New(), uuid.New(), kindText, "Название", "бриф", nil, gen.ModeTraining, nil)

	if gener.Calls != maxAttempts {
		t.Fatalf("ожидалось %d вызова генератора, получено %d", maxAttempts, gener.Calls)
	}
	if len(gener.Requests[1].Problems) == 0 {
		t.Fatal("второй вызов должен нести диагностику первой попытки")
	}
	if len(gener.Requests[2].Problems) == 0 {
		t.Fatal("третий вызов должен нести диагностику второй попытки")
	}

	st := fa.snapshotState(t)
	if st.Status != "done" {
		t.Fatalf("даже с оставшимися ошибками результат сохраняется как done, получено %q", st.Status)
	}
	if st.Check == nil || st.Check.Blocking == 0 {
		t.Fatalf("check должен нести блокирующие ошибки последней попытки: %+v", st.Check)
	}
	if fa.draftDoc() == nil {
		t.Fatal("документ с диагностикой всё равно должен попасть в черновик")
	}
}

// TestRunNotDocumentFailsAfterAllAttempts — модель трижды вернула валидный
// JSON, но не документ (массив) → провал, черновик не тронут.
func TestRunNotDocumentFailsAfterAllAttempts(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{
		{Document: []byte(`[]`)}, {Document: []byte(`[]`)}, {Document: []byte(`[]`)},
	}}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	svc.run(uuid.New(), uuid.New(), kindText, "Название", "бриф", nil, gen.ModeTraining, nil)

	if gener.Calls != maxAttempts {
		t.Fatalf("ожидалось %d вызова генератора, получено %d", maxAttempts, gener.Calls)
	}
	st := fa.snapshotState(t)
	if st.Status != "failed" {
		t.Fatalf("ожидался status=failed, получено %q", st.Status)
	}
	if st.Error == nil || st.Error.Message != "Модель вернула не документ сценария — повторите или создайте формой." {
		t.Fatalf("неверное сообщение об ошибке: %+v", st.Error)
	}
	if fa.draftDoc() != nil {
		t.Fatal("черновик не должен быть тронут при провале")
	}
}

// TestRunTimeoutFails — модель не отвечает (ErrTimeout) все три раза.
func TestRunTimeoutFails(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{
		{Err: ai.ErrTimeout}, {Err: ai.ErrTimeout}, {Err: ai.ErrTimeout},
	}}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	svc.run(uuid.New(), uuid.New(), kindText, "Название", "бриф", nil, gen.ModeTraining, nil)

	st := fa.snapshotState(t)
	if st.Status != "failed" {
		t.Fatalf("ожидался status=failed, получено %q", st.Status)
	}
	if st.Error == nil || st.Error.Message != "Модель не ответила за 3 минуты — повторите." {
		t.Fatalf("неверное сообщение об ошибке: %+v", st.Error)
	}
	if fa.draftDoc() != nil {
		t.Fatal("черновик не должен быть тронут при провале")
	}
}

// TestRunVoiceUsesTranscript — расшифровка передаётся модели как описание
// и попадает в authoring.brief и в transcript ответа.
func TestRunVoiceUsesTranscript(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	tpl := salesTemplate(t)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{{Document: tpl}}}
	tr := &ai.FakeTranscriber{Responses: []ai.FakeTranscriberResponse{{Text: "надиктованное описание"}}}
	svc := newService(fa, tr, gener, testLogger(), context.Background())

	svc.run(uuid.New(), uuid.New(), kindAudio, "Название", "", []byte("mp3-bytes"), gen.ModeTraining, nil)

	if len(tr.Received) != 1 || string(tr.Received[0]) != "mp3-bytes" {
		t.Fatalf("аудио должно уйти расшифровщику как есть: %+v", tr.Received)
	}
	if len(gener.Requests) != 1 || gener.Requests[0].Brief != "надиктованное описание" {
		t.Fatalf("расшифровка должна уйти генератору как описание: %+v", gener.Requests)
	}
	st := fa.snapshotState(t)
	if st.Status != "done" {
		t.Fatalf("ожидался status=done, получено %q", st.Status)
	}
	if st.Transcript != "надиктованное описание" {
		t.Fatalf("transcript должен быть заполнен: %+v", st)
	}
}

// TestRunEditUsesBaseDocument — правка передаёт текущий документ моделью
// как base, и forcePassport не трогает authoring.brief при правке.
func TestRunEditUsesBaseDocument(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	tpl := salesTemplate(t)
	gener := &ai.FakeGenerator{Responses: []ai.FakeGeneratorResponse{{Document: tpl}}}
	svc := newService(fa, nil, gener, testLogger(), context.Background())

	base := []byte(`{"passport":{"title":"старое название","mode":"training"},"authoring":{"brief":"старое описание"}}`)
	svc.run(uuid.New(), uuid.New(), kindTextEdit, "Название", "смени возражение оппонента", nil, gen.ModeTraining, base)

	if len(gener.Requests) != 1 {
		t.Fatalf("ожидался один вызов генератора, получено %d", len(gener.Requests))
	}
	if string(gener.Requests[0].Base) != string(base) {
		t.Fatalf("текущий документ должен уйти генератору как base: %s", gener.Requests[0].Base)
	}
	if gener.Requests[0].Brief != "смени возражение оппонента" {
		t.Fatalf("указание должно уйти генератору как brief-параметр: %q", gener.Requests[0].Brief)
	}

	var doc struct {
		Authoring struct {
			Brief string `json:"brief"`
		} `json:"authoring"`
	}
	if err := json.Unmarshal(fa.draftDoc(), &doc); err != nil {
		t.Fatalf("документ не разобрался: %v", err)
	}
	// forcePassport не трогает authoring на правке (isEdit=true) — то, что
	// вернула модель (в тесте — authoring шаблона), должно дойти как есть,
	// а не быть подменено указанием на правку или названием сценария.
	if doc.Authoring.Brief != "Типовой шаблон продажи услуги для библиотеки сценариев." {
		t.Fatalf("authoring.brief не должен подменяться при правке: %q", doc.Authoring.Brief)
	}
}

// --- recover ---

func TestRecoverCallsFailRunningGenerations(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	svc := newService(fa, nil, nil, testLogger(), context.Background())
	if err := svc.recover(context.Background()); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(fa.failMessages) != 1 || fa.failMessages[0] != "Портал перезапускался — повторите." {
		t.Fatalf("неверное сообщение при закрытии зависших заданий: %v", fa.failMessages)
	}
}

// --- snapshot / GET-представление ---

func TestSnapshotNilGenerationIsIdle(t *testing.T) {
	fa := newFakeAuthoring(gen.ModeTraining)
	svc := newService(fa, nil, nil, testLogger(), context.Background())

	body, err := svc.snapshot(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("сериализация: %v", err)
	}
	if string(raw) != `{"status":"idle"}` {
		t.Fatalf(`ожидалось {"status":"idle"} буквально, получено %s`, raw)
	}
}

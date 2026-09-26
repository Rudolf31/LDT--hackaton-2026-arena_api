package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/ratelimit"
)

// maxAudioBytes — 25 МБ на запись (arena-portal-hr.md 8.1); multipartOverhead
// даёт небольшой запас http.MaxBytesReader на служебные части multipart
// (границы, заголовки) сверх самого файла — сам файл всё равно режется
// ровно по maxAudioBytes ниже, через io.LimitReader.
const (
	maxAudioBytes     = 25 << 20
	multipartOverhead = 64 << 10
	maxTextBodyBytes  = 128 << 10 // с запасом на 20000 знаков UTF-8
)

// Transport — четыре ручных адреса авторства (D-05) плюс GetGeneration для
// адаптера cmd/portal/api.go (операция входит в контракт, роль проверяет
// strict-middleware auth по x-roles).
type Transport struct {
	service *service
	limiter *ratelimit.Limiter
	logger  *slog.Logger
}

// GetGeneration — единственная операция этого модуля в контракте (D-31):
// свой тип ответа, реализующий gen.GetGenerationResponseObject, вместо
// устаревшего gen.GenerationState из анкеты в десять полей.
func (t *Transport) GetGeneration(ctx context.Context, request gen.GetGenerationRequestObject) (gen.GetGenerationResponseObject, error) {
	if _, err := currentActor(ctx); err != nil {
		return nil, err
	}
	body, err := t.service.snapshot(ctx, request.ScenarioId)
	if err != nil {
		return nil, err
	}
	return getGenerationResponse{body: body}, nil
}

// getGenerationResponse — 200 JSON с самодельным телом (D-31): у операции
// нет годного сгенерированного типа ответа (контракт всё ещё несёт форму
// из отменённой анкеты, CLAUDE.md «Известные расхождения»).
type getGenerationResponse struct {
	body generationResponseBody
}

func (r getGenerationResponse) VisitGetGenerationResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.body)
}

// --- четыре ручных адреса (D-05) ---

func (t *Transport) PostGenerationAudio(w http.ResponseWriter, r *http.Request) {
	t.startVoice(w, r, kindAudio)
}

func (t *Transport) PostDraftVoiceEdit(w http.ResponseWriter, r *http.Request) {
	t.startVoice(w, r, kindVoiceEdit)
}

func (t *Transport) PostGenerationText(w http.ResponseWriter, r *http.Request) {
	t.startText(w, r, kindText, "text", 1, 20000)
}

func (t *Transport) PostDraftTextEdit(w http.ResponseWriter, r *http.Request) {
	t.startText(w, r, kindTextEdit, "instruction", 1, 4000)
}

func (t *Transport) startVoice(w http.ResponseWriter, r *http.Request, kind inputKind) {
	a, id, ok := t.prepare(w, r)
	if !ok {
		return
	}
	audio, err := readAudioPart(w, r)
	if err != nil {
		t.writeError(w, err)
		return
	}
	t.start(w, r.Context(), a, id, kind, "", audio)
}

func (t *Transport) startText(w http.ResponseWriter, r *http.Request, kind inputKind, field string, minLen, maxLen int) {
	a, id, ok := t.prepare(w, r)
	if !ok {
		return
	}
	text, err := readTextField(w, r, field, minLen, maxLen)
	if err != nil {
		t.writeError(w, err)
		return
	}
	t.start(w, r.Context(), a, id, kind, text, nil)
}

// prepare — то, что общее всем четырём адресам до чтения тела: разбор
// scenarioId из пути и частота (D-05: «общий предел на все четыре
// адреса», ratelimit.Limiters.Generation). Роль и вход уже проверил
// auth.PortalMiddleware (cmd/portal/router.go) — actor.From тут не может
// не сработать, но проверяем и его, как везде (CLAUDE.md, правило 8: без
// молчаливых веток).
func (t *Transport) prepare(w http.ResponseWriter, r *http.Request) (actor.Actor, uuid.UUID, bool) {
	a, err := currentActor(r.Context())
	if err != nil {
		t.writeError(w, err)
		return actor.Actor{}, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "scenarioId"))
	if err != nil {
		t.writeError(w, httpx.NewError(httpx.KindNotFound, "Такого сценария нет."))
		return actor.Actor{}, uuid.Nil, false
	}
	if ok, retryAfter := t.limiter.Allow(a.UserID.String()); !ok {
		t.writeError(w, httpx.NewError(httpx.KindRateLimited, "Слишком много запросов на генерацию. Подождите.").
			WithRetryAfter(int(math.Ceil(retryAfter.Seconds()))))
		return actor.Actor{}, uuid.Nil, false
	}
	return a, id, true
}

func (t *Transport) start(w http.ResponseWriter, ctx context.Context, a actor.Actor, id uuid.UUID, kind inputKind, text string, audio []byte) {
	body, err := t.service.Start(ctx, a, id, kind, text, audio)
	if err != nil {
		t.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(body)
}

func (t *Transport) writeError(w http.ResponseWriter, err error) {
	var httpErr *httpx.Error
	if errors.As(err, &httpErr) {
		httpx.WriteError(w, httpErr)
		return
	}
	t.logger.Error("generation: обработка запроса", "error", err.Error())
	httpx.WriteError(w, httpx.NewError(httpx.KindInternal, "Что-то пошло не так на сервере. Попробуйте ещё раз."))
}

// readAudioPart читает ровно одну часть multipart с именем поля "file" в
// память, с лимитом maxAudioBytes (05-generation.md, «ловушки»): никакого
// r.ParseMultipartForm/r.FormFile — они пишут во временные файлы, а mp3
// нигде не сохраняется (I-11). Ссылка на прочитанные байты нигде не
// удерживается дольше самого запроса — вызывающая сторона (Transcribe)
// передаёт их дальше и не кладёт в generation ничего, кроме расшифровки.
func readAudioPart(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAudioBytes+multipartOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, httpx.NewError(httpx.KindInvalidBody, "Запрос должен быть multipart/form-data с полем file.")
	}
	part, err := reader.NextPart()
	if err != nil {
		return nil, httpx.NewError(httpx.KindInvalidBody, "В запросе нет файла записи.")
	}
	defer part.Close()
	if part.FormName() != "file" {
		return nil, httpx.NewError(httpx.KindInvalidBody, "Поле файла должно называться file.")
	}

	data, err := io.ReadAll(io.LimitReader(part, maxAudioBytes+1))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, httpx.NewError(httpx.KindTooLarge, "Запись длиннее 25 МБ.")
		}
		return nil, httpx.NewError(httpx.KindInvalidBody, "Не удалось прочитать файл записи.")
	}
	if len(data) == 0 {
		return nil, httpx.NewError(httpx.KindInvalidBody, "Файл записи пуст.")
	}
	if len(data) > maxAudioBytes {
		return nil, httpx.NewError(httpx.KindTooLarge, "Запись длиннее 25 МБ.")
	}
	return data, nil
}

// readTextField читает тело JSON вручную (эти адреса вне сгенерированного
// роутера и его серединного ПО httpx.Body, D-05) в том же порядке: лимит
// размера → фильтр emotion — до записи тела куда-либо, включая лог (I-1) —
// → закрытая схема одного строкового поля с проверкой длины.
func readTextField(w http.ResponseWriter, r *http.Request, field string, minLen, maxLen int) (string, error) {
	if ct := contentType(r); ct != "application/json" {
		return "", httpx.NewError(httpx.KindInvalidBody, "Запрос принимается только с телом application/json.")
	}

	limited := http.MaxBytesReader(w, r.Body, maxTextBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return "", httpx.NewError(httpx.KindTooLarge, "Тело запроса больше допустимого предела.")
		}
		return "", httpx.NewError(httpx.KindInvalidBody, "Не удалось прочитать тело запроса.")
	}

	var parsed any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return "", httpx.NewError(httpx.KindInvalidBody, "Тело запроса — не корректный JSON.")
	}
	if dec.More() {
		return "", httpx.NewError(httpx.KindInvalidBody, "После тела запроса лишние данные.")
	}

	// Фильтр emotion — до записи тела куда-либо, включая лог (I-1, FR-RS-02):
	// та же проверка, что httpx.Body делает для сгенерированного роутера,
	// здесь она нужна отдельно — эти четыре адреса его middleware не проходят.
	if pointer, found := httpx.FindEmotionKey(parsed); found {
		return "", httpx.NewError(
			httpx.KindInvalidBody,
			"Полю с именем «emotion» в теле запроса взяться неоткуда — эмоций участника API не принимает.",
		).WithErrors([]gen.FieldError{{Path: pointer, Message: "ключ emotion недопустим на любой глубине тела"}})
	}

	obj, ok := parsed.(map[string]any)
	if !ok || len(obj) != 1 {
		return "", httpx.NewError(httpx.KindInvalidBody, "Тело запроса должно быть объектом с единственным полем "+field+".")
	}
	rawValue, ok := obj[field]
	if !ok {
		return "", httpx.NewError(httpx.KindInvalidBody, "В теле запроса нет поля "+field+".")
	}
	text, ok := rawValue.(string)
	if !ok {
		return "", httpx.NewError(httpx.KindInvalidBody, "Поле "+field+" должно быть строкой").
			WithErrors([]gen.FieldError{{Path: "/" + field, Message: "должно быть строкой"}})
	}
	if n := utf8.RuneCountInString(text); n < minLen || n > maxLen {
		return "", httpx.NewError(httpx.KindInvalidBody, "Поле "+field+" — недопустимой длины.").
			WithErrors([]gen.FieldError{{Path: "/" + field, Message: lengthMessage(minLen, maxLen)}})
	}
	return text, nil
}

func lengthMessage(minLen, maxLen int) string {
	if minLen <= 1 {
		return "длина не больше " + strconv.Itoa(maxLen) + " знаков"
	}
	return "длина от " + strconv.Itoa(minLen) + " до " + strconv.Itoa(maxLen) + " знаков"
}

func contentType(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	for i, c := range ct {
		if c == ';' {
			return ct[:i]
		}
	}
	return ct
}

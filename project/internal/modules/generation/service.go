package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/scenariodoc"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/ai"
	"arena-portal-backend/internal/platform/httpx"
)

// Таймауты и число попыток — «решения по умолчанию» плана этапа 05:
// расшифровка 90 с, один вызов генерации 180 с, всё задание 10 минут,
// три попытки генерации (первая + два повтора, arena-portal-hr.md 11.6).
const (
	transcribeTimeout = 90 * time.Second
	generateTimeout   = 180 * time.Second
	jobTimeout        = 10 * time.Minute
	maxAttempts       = 3
)

// inputKind — вид ввода задания (D-31: input_kind в GET /generation).
type inputKind string

const (
	kindAudio     inputKind = "audio"
	kindText      inputKind = "text"
	kindVoiceEdit inputKind = "voice_edit"
	kindTextEdit  inputKind = "text_edit"
)

func (k inputKind) isEdit() bool  { return k == kindVoiceEdit || k == kindTextEdit }
func (k inputKind) isVoice() bool { return k == kindAudio || k == kindVoiceEdit }

// jobState — то, что лежит в scenarios.generation целиком (формат решает
// этот модуль, D-31): персональные данные в нём быть не может — участник
// тут ни при чём, это всегда методолог и его собственный текст описания
// или указания на правку.
type jobState struct {
	// Title — название сценария, заданное при POST /scenarios (D-26); не
	// входит в ответ GET /generation (там его нет в перечне D-31) и
	// переносится из предыдущего generation при каждом обновлении
	// («решения по умолчанию» плана этапа).
	Title       string           `json:"title,omitempty"`
	Status      string           `json:"status"`
	InputKind   inputKind        `json:"input_kind,omitempty"`
	Stage       string           `json:"stage,omitempty"`
	Attempts    int              `json:"attempts,omitempty"`
	Transcript  string           `json:"transcript,omitempty"`
	Instruction string           `json:"instruction,omitempty"`
	Check       *gen.CheckResult `json:"check,omitempty"`
	Error       *jobError        `json:"error,omitempty"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
}

type jobError struct {
	Message           string `json:"message"`
	RetryAfterSeconds *int   `json:"retry_after_seconds"`
}

// generationResponseBody — тело GET /generation и 202 на запуск (D-31):
// то же jobState, спроецированное для экрана (без Title — он не входит в
// перечень полей ответа) плюс scenario_id из запроса. generation = NULL →
// {"status":"idle"} буквально, без остальных полей (решение D-31).
type generationResponseBody struct {
	ScenarioID  *uuid.UUID       `json:"scenario_id,omitempty"`
	InputKind   *inputKind       `json:"input_kind,omitempty"`
	Status      string           `json:"status"`
	Stage       *string          `json:"stage,omitempty"`
	Attempts    *int             `json:"attempts,omitempty"`
	Transcript  *string          `json:"transcript,omitempty"`
	Instruction *string          `json:"instruction,omitempty"`
	Check       *gen.CheckResult `json:"check,omitempty"`
	Error       *jobError        `json:"error,omitempty"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
}

// service держит baseCtx намеренно (контекст процесса, не запроса):
// задание фонового job'а живёт дольше HTTP-запроса, который его запустил
// (план этапа 05, «решения по умолчанию»), и отменяется при остановке
// портала — оттуда и берётся дедлайн для jobTimeout в run().
type service struct {
	scenarios   scenarios.Authoring
	transcriber ai.Transcriber
	generator   ai.ScenarioGenerator
	logger      *slog.Logger
	baseCtx     context.Context
}

func newService(scenariosAuthoring scenarios.Authoring, transcriber ai.Transcriber, generator ai.ScenarioGenerator,
	logger *slog.Logger, baseCtx context.Context,
) *service {
	return &service{scenarios: scenariosAuthoring, transcriber: transcriber, generator: generator, logger: logger, baseCtx: baseCtx}
}

func errSTTUnavailable() *httpx.Error {
	return httpx.NewError(httpx.KindGenerationUnavailable, "Расшифровка речи не настроена.")
}

func errGenUnavailable() *httpx.Error {
	return httpx.NewError(httpx.KindGenerationUnavailable, "Модель генерации не настроена.")
}

// currentActor — та же проверка, что в других модулях (auth уже
// подтвердил вход через PortalMiddleware/strict Middleware, здесь — просто
// чтение из контекста).
func currentActor(ctx context.Context) (actor.Actor, error) {
	a, ok := actor.From(ctx)
	if !ok {
		return actor.Actor{}, httpx.NewError(httpx.KindUnauthenticated, "Войдите в портал.")
	}
	return a, nil
}

// snapshot — тело GET /generation: разбирает scenarios.generation в
// jobState и проецирует в ответ (D-31).
func (s *service) snapshot(ctx context.Context, id uuid.UUID) (generationResponseBody, error) {
	snap, err := s.scenarios.GenerationState(ctx, id)
	if err != nil {
		return generationResponseBody{}, err
	}
	if snap.State == nil {
		return generationResponseBody{Status: "idle"}, nil
	}
	var st jobState
	if err := json.Unmarshal(snap.State, &st); err != nil {
		return generationResponseBody{}, fmt.Errorf("разбор состояния авторства сценария %s: %w", id, err)
	}
	return toResponseBody(id, st), nil
}

func toResponseBody(id uuid.UUID, st jobState) generationResponseBody {
	body := generationResponseBody{ScenarioID: &id, Status: orIdle(st.Status)}
	if st.InputKind != "" {
		k := st.InputKind
		body.InputKind = &k
	}
	if st.Stage != "" {
		stage := st.Stage
		body.Stage = &stage
	}
	if st.Status != "" {
		attempts := st.Attempts
		body.Attempts = &attempts
	}
	if st.Transcript != "" {
		t := st.Transcript
		body.Transcript = &t
	}
	if st.Instruction != "" {
		i := st.Instruction
		body.Instruction = &i
	}
	body.Check = st.Check
	body.Error = st.Error
	body.StartedAt = st.StartedAt
	body.FinishedAt = st.FinishedAt
	return body
}

func orIdle(status string) string {
	if status == "" {
		return "idle"
	}
	return status
}

// Start — запуск задания (UC-M-01): 503, если нужная модель не настроена
// (D-34); дальше — scenarios.BeginGeneration решает 409/404, а сама работа
// уходит в фон на s.baseCtx (не на контексте запроса — ответ 202 уходит
// сразу, задание должно пережить его). actorID нужен только на успешный
// исход (FinishGeneration → draft_updated_by).
func (s *service) Start(ctx context.Context, a actor.Actor, id uuid.UUID, kind inputKind, text string, audio []byte) (generationResponseBody, error) {
	if kind.isVoice() && s.transcriber == nil {
		return generationResponseBody{}, errSTTUnavailable()
	}
	if s.generator == nil {
		return generationResponseBody{}, errGenUnavailable()
	}

	snap, err := s.scenarios.GenerationState(ctx, id)
	if err != nil {
		return generationResponseBody{}, err
	}
	title := ""
	if snap.State != nil {
		var cur jobState
		if err := json.Unmarshal(snap.State, &cur); err == nil {
			title = cur.Title
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	initial := jobState{Title: title, Status: "running", InputKind: kind, StartedAt: &now}
	if kind.isVoice() {
		initial.Stage = "transcribing"
	} else {
		initial.Stage = "generating"
	}
	if !kind.isVoice() {
		// Текстовый ввод сразу несёт то, что укажет диагностике на экране,
		// пока идёт первая попытка генерации (расшифровки, которую можно
		// было бы показать вместо этого, тут нет).
		if kind.isEdit() {
			initial.Instruction = text
		}
	}
	raw, err := json.Marshal(initial)
	if err != nil {
		return generationResponseBody{}, fmt.Errorf("сериализация состояния авторства: %w", err)
	}

	if err := s.scenarios.BeginGeneration(ctx, id, kind.isEdit(), raw); err != nil {
		return generationResponseBody{}, err
	}

	go s.run(id, a.UserID, kind, title, text, audio, snap.Mode, snap.Draft)

	return toResponseBody(id, initial), nil
}

// run — тело задания: transcribing (только голос) → generating → checking,
// до maxAttempts вызовов модели генерации. Выполняется на s.baseCtx —
// переживает HTTP-запрос, который его запустил, и обрывается при
// остановке портала (Recover при следующем старте закрывает то, что не
// успело завершиться, в failed).
func (s *service) run(id, actorID uuid.UUID, kind inputKind, title, text string, audio []byte, mode gen.Mode, base json.RawMessage) {
	ctx, cancel := context.WithTimeout(s.baseCtx, jobTimeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			// Правило 5/8: в лог — только факт паники, без содержимого
			// (описания, указания, документа).
			s.logger.Error("generation: паника в задании", "scenario_id", id.String(), "panic", fmt.Sprint(r))
			s.finishFailed(context.WithoutCancel(ctx), id, actorID, title, kind, 0, "Что-то пошло не так при генерации — повторите.")
		}
	}()

	transcript := ""
	instruction := ""
	if kind.isEdit() && !kind.isVoice() {
		instruction = text
	}

	if kind.isVoice() {
		st := jobState{Title: title, Status: "running", InputKind: kind, Stage: "transcribing"}
		s.persist(ctx, id, st)

		tctx, tcancel := context.WithTimeout(ctx, transcribeTimeout)
		text0, err := s.transcriber.Transcribe(tctx, bytes.NewReader(audio))
		tcancel()
		if err != nil {
			s.logger.Warn("generation: расшифровка не удалась", "scenario_id", id.String(), "input_kind", string(kind))
			s.finishFailed(ctx, id, actorID, title, kind, 0, "Расшифровка не удалась — повторите.")
			return
		}
		transcript = text0
		if kind.isEdit() {
			instruction = transcript
		}
	}

	brief := text
	if kind.isVoice() {
		brief = transcript
	}
	promptText := brief
	if kind.isEdit() {
		promptText = instruction
	}

	var problems []string
	var finalDoc []byte
	var finalDiags []scenariodoc.Diagnostic
	timedOut := false
	attempts := 0

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		s.persist(ctx, id, jobState{
			Title: title, Status: "running", InputKind: kind, Stage: "generating", Attempts: attempt,
			Transcript: transcript, Instruction: instruction,
		})

		gctx, gcancel := context.WithTimeout(ctx, generateTimeout)
		doc, err := s.generator.Generate(gctx, promptText, base, problems)
		gcancel()
		if err != nil {
			if errors.Is(err, ai.ErrTimeout) {
				timedOut = true
			}
			s.logger.Warn("generation: попытка генерации не удалась",
				"scenario_id", id.String(), "attempt", attempt, "timeout", errors.Is(err, ai.ErrTimeout))
			if attempt == maxAttempts {
				break
			}
			continue
		}

		shaped, shapeErr := forcePassport(doc, mode, kind, title, brief)
		if shapeErr != nil {
			s.logger.Warn("generation: ответ модели не документ сценария", "scenario_id", id.String(), "attempt", attempt)
			if attempt == maxAttempts {
				break
			}
			continue
		}

		s.persist(ctx, id, jobState{
			Title: title, Status: "running", InputKind: kind, Stage: "checking", Attempts: attempt,
			Transcript: transcript, Instruction: instruction,
		})

		diags := scenariodoc.Validate(shaped)
		if !scenariodoc.Publishable(diags) && attempt < maxAttempts {
			problems = blockingMessages(diags)
			continue
		}

		finalDoc = shaped
		finalDiags = diags
		break
	}

	if finalDoc == nil {
		message := "Модель вернула не документ сценария — повторите или создайте формой."
		if timedOut {
			message = "Модель не ответила за 3 минуты — повторите."
		}
		s.finishFailed(ctx, id, actorID, title, kind, attempts, message)
		return
	}

	check := toCheckResult(finalDiags)
	now := time.Now().UTC().Truncate(time.Microsecond)
	final := jobState{
		Title: title, Status: "done", InputKind: kind, Attempts: attempts,
		Transcript: transcript, Instruction: instruction, Check: &check, FinishedAt: &now,
	}
	raw, err := json.Marshal(final)
	if err != nil {
		s.logger.Error("generation: сериализация итогового состояния", "scenario_id", id.String(), "error", err.Error())
		return
	}
	if err := s.scenarios.FinishGeneration(ctx, id, raw, finalDoc, actorID); err != nil {
		s.logger.Error("generation: запись результата", "scenario_id", id.String(), "error", err.Error())
	}
}

func (s *service) persist(ctx context.Context, id uuid.UUID, st jobState) {
	raw, err := json.Marshal(st)
	if err != nil {
		s.logger.Error("generation: сериализация состояния", "scenario_id", id.String(), "error", err.Error())
		return
	}
	if err := s.scenarios.UpdateGeneration(ctx, id, raw); err != nil {
		s.logger.Error("generation: запись состояния", "scenario_id", id.String(), "error", err.Error())
	}
}

func (s *service) finishFailed(ctx context.Context, id, actorID uuid.UUID, title string, kind inputKind, attempts int, message string) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	st := jobState{
		Title: title, Status: "failed", InputKind: kind, Attempts: attempts,
		Error: &jobError{Message: message}, FinishedAt: &now,
	}
	raw, err := json.Marshal(st)
	if err != nil {
		s.logger.Error("generation: сериализация отказа", "scenario_id", id.String(), "error", err.Error())
		return
	}
	if err := s.scenarios.FinishGeneration(ctx, id, raw, nil, actorID); err != nil {
		s.logger.Error("generation: запись отказа", "scenario_id", id.String(), "error", err.Error())
	}
}

// recover — при старте портала переводит зависшие running в failed
// (arena-api.yaml, getGeneration: «портал закрывает зависшие задания»).
func (s *service) recover(ctx context.Context) error {
	n, err := s.scenarios.FailRunningGenerations(ctx, "Портал перезапускался — повторите.")
	if err != nil {
		return err
	}
	if n > 0 {
		s.logger.Warn("generation: закрыты зависшие задания при перезапуске портала", "count", n)
	}
	return nil
}

// forcePassport приводит passport.mode результата к режиму сценария и (на
// создании, не на правке) passport.title к названию из generation.title и
// authoring.brief к описанию, с которого начался сценарий («решения по
// умолчанию» плана этапа 05). Ошибка — модель вернула валидный JSON, но не
// объект документа (например, массив или строку): это отдельная причина
// провала попытки, не то же самое, что ошибки scenariodoc.Validate.
func forcePassport(document []byte, mode gen.Mode, kind inputKind, title, brief string) ([]byte, error) {
	var tree map[string]any
	if err := json.Unmarshal(document, &tree); err != nil {
		return nil, fmt.Errorf("ответ модели — не объект документа: %w", err)
	}
	passport, _ := tree["passport"].(map[string]any)
	if passport == nil {
		passport = map[string]any{}
	}
	passport["mode"] = string(mode)
	if !kind.isEdit() {
		if strings.TrimSpace(title) != "" {
			passport["title"] = title
		}
		authoring, _ := tree["authoring"].(map[string]any)
		if authoring == nil {
			authoring = map[string]any{}
		}
		authoring["brief"] = brief
		tree["authoring"] = authoring
	}
	tree["passport"] = passport
	return json.Marshal(tree)
}

// blockingMessages — диагностика прошлой попытки текстом «путь: сообщение»
// для следующего вызова модели (D-33); только блокирующие — предупреждения
// модели показывать незачем, они не мешают сохранить документ.
func blockingMessages(diags []scenariodoc.Diagnostic) []string {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Severity == scenariodoc.SeverityError {
			msgs = append(msgs, d.Path+": "+d.Message)
		}
	}
	return msgs
}

// toCheckResult — то же самое преобразование, что делает
// internal/modules/scenarios/service.go для draft_check: сюда не
// импортируется (правило CLAUDE.md «к чужим таблицам/внутренностям не
// ходим» — это внутренняя функция другого модуля), поэтому короткая копия
// здесь же.
func toCheckResult(diags []scenariodoc.Diagnostic) gen.CheckResult {
	messages := make([]gen.CheckMessage, 0, len(diags))
	blocking := 0
	for _, d := range diags {
		msg := gen.CheckMessage{Message: d.Message, Path: d.Path, Severity: gen.Warning}
		if d.Severity == scenariodoc.SeverityError {
			msg.Severity = gen.Blocking
			blocking++
		}
		if d.Rule != "" {
			rule := d.Rule
			msg.Rule = &rule
		}
		if d.Level != "" {
			level := gen.Difficulty(d.Level)
			msg.Difficulty = &level
		}
		messages = append(messages, msg)
	}
	return gen.CheckResult{Blocking: blocking, EngineVersion: scenariodoc.EngineVersion, Messages: messages}
}

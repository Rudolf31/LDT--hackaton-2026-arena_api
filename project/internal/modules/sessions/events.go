package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

// Приём событий хода (архитектура 7). Тело уже прошло закрытую схему
// контракта и фильтр ключа emotion (httpx.Body, I-1); разбор здесь — из
// сырого тела, потому что сгенерированный тип не отличает null от
// отсутствия поля и теряет объекты движка.

type turnEvent struct {
	Seq                  int             `json:"seq"`
	Speaker              string          `json:"speaker"`
	ReplyNo              int             `json:"reply_no"`
	Text                 string          `json:"text"`
	AtMs                 int             `json:"at_ms"`
	DurationMs           *int            `json:"duration_ms"`
	PauseMs              *int            `json:"pause_ms"`
	ModelRoute           *string         `json:"model_route"`
	Stage                string          `json:"stage"`
	TransitionTo         *string         `json:"transition_to"`
	MoveType             *string         `json:"move_type"`
	MoveSource           *string         `json:"move_source"`
	JudgeConfidence      *float64        `json:"judge_confidence"`
	Evidence             *string         `json:"evidence"`
	Interest             *string         `json:"interest"`
	Violations           []string        `json:"violations"`
	Judge                json.RawMessage `json:"judge"`
	Terms                json.RawMessage `json:"terms"`
	Trust                *int            `json:"trust"`
	Pressure             *int            `json:"pressure"`
	Credibility          *int            `json:"credibility"`
	Patience             *int            `json:"patience"`
	Credit               *int            `json:"credit"`
	RevealedFacts        []string        `json:"revealed_facts"`
	EngineStep           json.RawMessage `json:"engine_step"`
	Intent               *string         `json:"intent"`
	Offer                json.RawMessage `json:"offer"`
	OpponentJudge        json.RawMessage `json:"opponent_judge"`
	OpponentViolation    bool            `json:"opponent_violation"`
	OpponentJudgeComment *string         `json:"opponent_judge_comment"`
	Interrupted          bool            `json:"interrupted"`
}

type incidentEvent struct {
	EventID   string `json:"event_id"`
	Component string `json:"component"`
}

// EventsResult — ответ на пачку (arena-api.yaml, EventBatchResult).
type EventsResult struct {
	Accepted      int
	Duplicates    int
	ContiguousSeq int
	MissingSeqs   []int
	Status        string
	Reopened      bool
	Note          *string
}

// ownSession — сессия этого участника по этому назначению под блокировкой.
// Назначение не сверяется с базой: после отмены назначения или отзыва
// кода идущая сессия доигрывается (архитектура 9.4). Чужая сессия
// неотличима от несуществующей.
func (s *service) ownSession(ctx context.Context, tx pgx.Tx, t actor.Trainer, id uuid.UUID) (sessionRow, error) {
	row, err := s.store.lock(ctx, tx, id)
	if errors.Is(err, errSessionNotFound) {
		return sessionRow{}, errSessionMissing()
	}
	if err != nil {
		return sessionRow{}, err
	}
	if row.SubjectID != *t.SubjectID || row.AssignmentID == nil || *row.AssignmentID != *t.AssignmentID {
		return sessionRow{}, errSessionMissing()
	}
	return row, nil
}

func isClosed(status string) bool {
	return status != string(gen.SessionStatusInProgress) && status != string(gen.SessionStatusAbandoned)
}

// PostEvents — пачка событий хода (архитектура 7.2).
func (s *service) PostEvents(ctx context.Context, sessionID uuid.UUID, raw []byte) (EventsResult, error) {
	t, err := participant(ctx)
	if err != nil {
		return EventsResult{}, err
	}
	turns, incidents, err := parseBatch(raw)
	if err != nil {
		return EventsResult{}, err
	}

	var out EventsResult
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.ownSession(ctx, tx, t, sessionID)
		if err != nil {
			return err
		}
		if isClosed(row.Status) {
			return httpx.NewError(httpx.KindSessionFinished, "Сессия уже завершена — новые реплики к ней не добавляются.")
		}

		key, err := s.people.DataKey(ctx, row.SubjectID)
		if errors.Is(err, people.ErrKeyDestroyed) {
			key = nil // согласие отозвано: ходы пишутся без текстов
		} else if err != nil {
			return err
		}
		rows, simplified, stub, err := encryptTurns(key, turns)
		if err != nil {
			return err
		}
		accepted, err := s.store.insertTurns(ctx, tx, row.ID, rows)
		if err != nil {
			return turnsViolation(err)
		}
		merged, added, stubIncident, err := mergeIncidents(row.Incidents, incidents)
		if err != nil {
			return err
		}
		if err := s.store.afterEvents(ctx, tx, row.ID, simplified, stub || stubIncident, merged); err != nil {
			return err
		}

		out = EventsResult{Accepted: accepted + added, Duplicates: len(turns) + len(incidents) - accepted - added, Status: row.Status}
		if row.Status == string(gen.SessionStatusAbandoned) && out.Accepted > 0 {
			if err := s.reopen(ctx, tx, row, &out); err != nil {
				return err
			}
		}

		seqs, err := s.store.seqs(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		out.ContiguousSeq, out.MissingSeqs = gaps(seqs)
		return nil
	})
	return out, err
}

// reopen — поздние события открывают прерванную сервером сессию (FR-ST-03).
// Если по назначению уже идёт другая сессия, база не даст вторую идущую:
// ходы остаются записанными, статус — «прервана».
func (s *service) reopen(ctx context.Context, tx pgx.Tx, row sessionRow, out *EventsResult) error {
	var count int
	err := pg.WithSavepoint(ctx, tx, "reopen", func(ctx context.Context) error {
		var err error
		count, err = s.store.reopen(ctx, tx, row.ID)
		return err
	})
	if v, ok := pg.AsViolation(err); ok && v.Kind == pg.Unique && v.Constraint == "sessions_one_running_per_assignment" {
		note := "По этому назначению уже идёт другая сессия — реплики сохранены, эта сессия остаётся прерванной."
		out.Note = &note
		return nil
	}
	if err != nil {
		return fmt.Errorf("повторное открытие сессии: %w", err)
	}
	sessionID, subjectID := row.ID, row.SubjectID
	if err := s.audit.Write(ctx, tx, audit.Entry{
		ActorKind: audit.ActorParticipant, Action: gen.AuditActionSessionReopened, Outcome: audit.OutcomeOK,
		SubjectID: &subjectID, SessionID: &sessionID, GroupID: row.GroupID,
		Details: map[string]any{"reopened_count": count},
	}); err != nil {
		return err
	}
	out.Status, out.Reopened = string(gen.SessionStatusInProgress), true
	return nil
}

// parseBatch разбирает пачку и проверяет то, что закрытая схема выразить
// не может: поля другой стороны у реплики и пояснение судьи оппонента не
// на своём месте.
func parseBatch(raw []byte) ([]turnEvent, []json.RawMessage, error) {
	var batch struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, nil, httpx.NewError(httpx.KindInvalidBody, "Пачка событий не разобралась.")
	}
	var turns []turnEvent
	var incidents []json.RawMessage
	var errs []gen.FieldError
	for i, ev := range batch.Events {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(ev, &head); err != nil {
			return nil, nil, httpx.NewError(httpx.KindInvalidBody, "Событие пачки не разобралось.")
		}
		if head.Type == "incident" {
			incidents = append(incidents, ev)
			continue
		}
		var te turnEvent
		if err := json.Unmarshal(ev, &te); err != nil {
			return nil, nil, httpx.NewError(httpx.KindInvalidBody, "Реплика в пачке не разобралась.")
		}
		te.Judge, te.Terms, te.EngineStep = nullToNil(te.Judge), nullToNil(te.Terms), nullToNil(te.EngineStep)
		te.Offer, te.OpponentJudge = nullToNil(te.Offer), nullToNil(te.OpponentJudge)
		if hasTopKey(te.OpponentJudge, "comment") {
			return nil, nil, httpx.NewError(httpx.KindInvalidBody,
				"Пояснение судьи оппонента передаётся только в opponent_judge_comment.").
				WithErrors([]gen.FieldError{{Path: fmt.Sprintf("/events/%d/opponent_judge/comment", i),
					Message: "Перенесите comment в opponent_judge_comment."}})
		}
		errs = append(errs, sideErrors(i, te)...)
		turns = append(turns, te)
	}
	if len(errs) > 0 {
		return nil, nil, httpx.NewError(httpx.KindValidationFailed, "В пачке есть реплики с полями не своей стороны.").WithErrors(errs)
	}
	return turns, incidents, nil
}

// sideErrors повторяет ограничения turns_* базы понятными фразами.
func sideErrors(i int, te turnEvent) []gen.FieldError {
	var errs []gen.FieldError
	add := func(field, msg string) {
		errs = append(errs, gen.FieldError{Path: fmt.Sprintf("/events/%d/%s", i, field), Message: msg})
	}
	if te.Speaker == string(gen.SpeakerParticipant) {
		if te.ReplyNo < 1 {
			add("reply_no", "Реплики участника нумеруются с единицы.")
		}
		if te.Intent != nil || te.Offer != nil || te.OpponentJudge != nil || te.OpponentViolation || te.Interrupted ||
			te.OpponentJudgeComment != nil {
			add("speaker", "У реплики участника не бывает намерения, предложения, решения судьи оппонента и перебивания.")
		}
	} else if te.MoveType != nil || te.MoveSource != nil || te.Judge != nil || te.Terms != nil {
		add("speaker", "У реплики оппонента не бывает типа хода, решения судьи участника и условий участника.")
	}
	if (te.MoveType == nil) != (te.MoveSource == nil) {
		add("move_source", "Тип хода и его источник приходят вместе.")
	}
	return errs
}

// encryptTurns готовит строки turns: тексты и пояснение судьи оппонента —
// шифром ключом участника (архитектура 9.1); ключ уничтожен — NULL.
func encryptTurns(key []byte, turns []turnEvent) ([]turnRow, bool, bool, error) {
	var simplified, stub bool
	rows := make([]turnRow, 0, len(turns))
	for _, te := range turns {
		row := turnRow{
			Seq: te.Seq, Speaker: te.Speaker, ReplyNo: te.ReplyNo, AtMs: te.AtMs, DurationMs: te.DurationMs,
			PauseMs: te.PauseMs, ModelRoute: te.ModelRoute, Stage: te.Stage, TransitionTo: te.TransitionTo,
			MoveType: te.MoveType, MoveSource: te.MoveSource, JudgeConfidence: te.JudgeConfidence,
			Evidence: te.Evidence, Interest: te.Interest, Violations: te.Violations, Judge: te.Judge,
			Terms: te.Terms, Trust: te.Trust, Pressure: te.Pressure, Credibility: te.Credibility,
			Patience: te.Patience, Credit: te.Credit, RevealedFacts: te.RevealedFacts, EngineStep: te.EngineStep,
			Intent: te.Intent, Offer: te.Offer, OpponentJudge: te.OpponentJudge,
			OpponentViolation: te.OpponentViolation, Interrupted: te.Interrupted,
		}
		if key != nil {
			enc, err := crypto.Encrypt(key, []byte(te.Text))
			if err != nil {
				return nil, false, false, fmt.Errorf("шифрование реплики: %w", err)
			}
			row.TextEnc = enc
			if te.OpponentJudgeComment != nil {
				if row.CommentEnc, err = crypto.Encrypt(key, []byte(*te.OpponentJudgeComment)); err != nil {
					return nil, false, false, fmt.Errorf("шифрование пояснения судьи: %w", err)
				}
			}
		}
		if te.MoveSource != nil && *te.MoveSource == string(gen.MoveSourceKeywords) {
			simplified = true
		}
		if te.ModelRoute != nil && *te.ModelRoute == string(gen.ModelRouteStub) {
			stub = true
		}
		rows = append(rows, row)
	}
	return rows, simplified, stub, nil
}

// mergeIncidents дописывает отказы компонентов в sessions.incidents без
// повторов по event_id. Возвращает новый список, число добавленных и был
// ли среди добавленных переход на заглушку.
func mergeIncidents(current []byte, incoming []json.RawMessage) ([]byte, int, bool, error) {
	var list []json.RawMessage
	if len(current) > 0 {
		if err := json.Unmarshal(current, &list); err != nil {
			return nil, 0, false, fmt.Errorf("отказы компонентов сессии: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, item := range list {
		var inc incidentEvent
		if err := json.Unmarshal(item, &inc); err == nil {
			seen[inc.EventID] = true
		}
	}
	added, stub := 0, false
	for _, item := range incoming {
		var inc incidentEvent
		if err := json.Unmarshal(item, &inc); err != nil {
			return nil, 0, false, httpx.NewError(httpx.KindInvalidBody, "Отказ компонента в пачке не разобрался.")
		}
		if seen[inc.EventID] {
			continue
		}
		seen[inc.EventID] = true
		list = append(list, item)
		added++
		if inc.Component == "stub" {
			stub = true
		}
	}
	if list == nil {
		list = []json.RawMessage{}
	}
	merged, err := json.Marshal(list)
	if err != nil {
		return nil, 0, false, fmt.Errorf("отказы компонентов сессии: %w", err)
	}
	return merged, added, stub, nil
}

// turnsViolation — нарушения ограничений turns как ошибка клиента, а не 500.
func turnsViolation(err error) error {
	v, ok := pg.AsViolation(err)
	if !ok {
		return fmt.Errorf("запись реплик: %w", err)
	}
	switch {
	case v.Kind == pg.Unique && v.Constraint == "turns_reply_key":
		return httpx.NewError(httpx.KindValidationFailed,
			"Реплика с этим номером у этой стороны уже записана под другим seq.")
	case v.Kind == pg.Check:
		return httpx.NewError(httpx.KindValidationFailed, "Реплика не прошла проверку записи: "+checkMessage(v.Constraint))
	}
	return fmt.Errorf("запись реплик: %w", err)
}

func checkMessage(constraint string) string {
	switch constraint {
	case "turns_timing":
		return "время и длительность не могут быть отрицательными."
	case "turns_confidence":
		return "уверенность судьи — от 0 до 1."
	case "turns_reply_no":
		return "номер реплики не может быть отрицательным, у участника — с единицы."
	}
	return "поля реплики противоречат друг другу."
}

// gaps — наибольший seq без пропусков до него (−1 — ходов нет или нет
// нулевого) и пропущенные номера ниже наибольшего пришедшего.
func gaps(seqs []int) (int, []int) {
	contiguous := -1
	missing := []int{}
	next := 0
	for _, seq := range seqs {
		for ; next < seq; next++ {
			missing = append(missing, next)
		}
		if len(missing) == 0 {
			contiguous = seq
		}
		next = seq + 1
	}
	return contiguous, missing
}

func nullToNil(raw json.RawMessage) json.RawMessage {
	if isNull(raw) {
		return nil
	}
	return raw
}

func hasTopKey(raw json.RawMessage, key string) bool {
	if raw == nil {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	_, ok := obj[key]
	return ok
}

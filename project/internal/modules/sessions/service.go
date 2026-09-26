package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/assignments"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenariodoc"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

// tokenTTL — срок токена участника (arena-portal-hr.md 8.1, «Вход»).
const tokenTTL = 12 * time.Hour

// defaultCriteriaSet — набор критериев «процесса», если в документе его
// нет (arena-db.md: sessions.criteria_set).
const defaultCriteriaSet = "harvard_spin_v1"

type service struct {
	pool     *pgxpool.Pool
	store    *store
	entry    assignments.Entry
	versions scenarios.Versions
	people   people.Service
	profiles profiles.Service
	consents consents.Service
	tokens   auth.TrainerTokens
	now      func() time.Time
}

// ConsentScreen и ProfileSnapshot — псевдонимы, чтобы transport.go не
// импортировал соседние модули (D-18).
type (
	ConsentScreen   = consents.Screen
	ProfileSnapshot = profiles.Snapshot
)

func errSessionMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Сессия не найдена.")
}

func errConsentRequired(title string) *httpx.Error {
	return httpx.NewError(httpx.KindConsentRequired, title)
}

func errWithdrawn() *httpx.Error {
	return httpx.NewError(httpx.KindAssignmentCancelled, "Согласие отозвано — назначение отменено.")
}

// participant — участник тренажёра из токена. Гость демо и репетиция
// входят по своим адресам (этапы 11 и 10).
func participant(ctx context.Context) (actor.Trainer, error) {
	t, ok := actor.TrainerFrom(ctx)
	if !ok {
		return actor.Trainer{}, httpx.NewError(httpx.KindUnauthenticated, "Откройте тренажёр по коду доступа.")
	}
	if t.Kind != string(auth.TrainerTokenParticipant) || t.AssignmentID == nil || t.SubjectID == nil {
		return actor.Trainer{}, httpx.NotImplemented()
	}
	return t, nil
}

// assignmentOf — назначение из токена, сверенное с базой: токен выдан
// этому участнику на это назначение.
func (s *service) assignmentOf(ctx context.Context, t actor.Trainer) (assignments.State, error) {
	state, err := s.entry.ForTrainer(ctx, *t.AssignmentID)
	if err != nil {
		return assignments.State{}, err
	}
	if state.SubjectID != *t.SubjectID {
		return assignments.State{}, httpx.NewError(httpx.KindUnauthenticated, "Откройте тренажёр по коду доступа ещё раз.")
	}
	return state, nil
}

// document — документ версии с применённым уровнем (FR-AC-01) и его
// части раздела 16 для полей контракта (D-50).
func document(v scenarios.VersionInfo, level gen.Difficulty) (json.RawMessage, scenariodoc.Parts, error) {
	doc, err := scenariodoc.ApplyDifficulty(v.Document, scenariodoc.Level(level))
	if err != nil {
		return nil, scenariodoc.Parts{}, fmt.Errorf("применение уровня к версии сценария: %w", err)
	}
	parts, err := scenariodoc.Split(doc, scenariodoc.Level(level), v.Fingerprint)
	if err != nil {
		return nil, scenariodoc.Parts{}, err
	}
	return doc, parts, nil
}

// --- вход по коду ---

// EnterResult — ответ запроса 1 (UC-P-01, UC-P-06).
type EnterResult struct {
	Token            string
	TokenExpiresAt   time.Time
	Number           string
	DisplayName      string
	AssignmentID     uuid.UUID
	Mode             gen.Mode
	Difficulty       gen.Difficulty
	DueAt            time.Time
	Next             gen.EnterResponseNext
	RunningSessionID *uuid.UUID
	HasResults       bool
	CanStartNew      bool
	Format           string
	EngineVersion    string
	Public           json.RawMessage
	Document         json.RawMessage
	Profile          ProfileSnapshot
	Screen           ConsentScreen
}

// Enter — код меняется на токен участника; в ответе документ с уровнем,
// профиль без ключей, экран согласия и что делать дальше.
func (s *service) Enter(ctx context.Context, code string) (EnterResult, error) {
	red, err := s.entry.Redeem(ctx, code)
	if err != nil {
		return EnterResult{}, err
	}
	version, err := s.versions.Version(ctx, red.VersionID)
	if err != nil {
		return EnterResult{}, err
	}
	person, err := s.people.Person(ctx, red.SubjectID)
	if errors.Is(err, people.ErrKeyDestroyed) {
		return EnterResult{}, errWithdrawn()
	}
	if err != nil {
		return EnterResult{}, err
	}
	refs, err := s.people.PersonRefs(ctx, []uuid.UUID{red.SubjectID})
	if err != nil {
		return EnterResult{}, err
	}
	past, err := s.store.byAssignments(ctx, s.pool, []uuid.UUID{red.AssignmentID})
	if err != nil {
		return EnterResult{}, err
	}
	doc, parts, err := document(version, red.Difficulty)
	if err != nil {
		return EnterResult{}, err
	}
	profile, err := s.profiles.ForClient(ctx, red.ProfileID, !person.ExternalAIWithdrawn)
	if err != nil {
		return EnterResult{}, err
	}
	assignmentID := red.AssignmentID
	screen, err := s.consents.TextsFor(ctx, consents.Audience{
		SubjectID: red.SubjectID, AssignmentID: &assignmentID, Mode: version.Mode, ProfileID: red.ProfileID,
	})
	if err != nil {
		return EnterResult{}, err
	}

	expires := s.now().Add(tokenTTL)
	subjectID, codeID := red.SubjectID, red.CodeID
	token, err := s.tokens.Issue(auth.TrainerClaims{
		Kind: auth.TrainerTokenParticipant, AssignmentID: &assignmentID, SubjectID: &subjectID,
		CodeID: &codeID, ExpiresAt: expires,
	})
	if err != nil {
		return EnterResult{}, err
	}

	out := EnterResult{
		Token: token, TokenExpiresAt: expires, Number: refs[red.SubjectID].Number,
		AssignmentID: assignmentID, Mode: version.Mode, Difficulty: red.Difficulty, DueAt: red.DueAt,
		Next: gen.EnterResponseNextBrief, CanStartNew: true,
		Format: version.Format, EngineVersion: version.EngineVersion,
		Public: parts.Public, Document: doc, Profile: profile, Screen: screen,
	}
	switch {
	case person.FullName != nil:
		out.DisplayName = *person.FullName
	case person.Pseudonym != nil:
		out.DisplayName = *person.Pseudonym
	}
	for _, p := range past {
		if p.Status == string(gen.SessionStatusInProgress) {
			id := p.ID
			out.RunningSessionID = &id
		} else {
			out.HasResults = true
		}
	}
	switch {
	case out.RunningSessionID != nil:
		out.Next = gen.EnterResponseNextResume
	case version.Mode == gen.ModeAssessment && len(past) > 0:
		// Оценочное назначение — одна попытка
		// (sessions_one_assessment_per_assignment).
		out.Next, out.CanStartNew = gen.EnterResponseNextResults, false
	}
	return out, nil
}

// --- старт и повторная выдача ---

// StartResult — ответ запроса 3 и повторной выдачи после F5.
type StartResult struct {
	Created          bool
	Session          sessionRow
	Private          json.RawMessage
	Document         json.RawMessage
	Profile          ProfileSnapshot
	OpenRouterKey    *string
	ModelServerToken *string
}

// Start — запрос 3 (UC-P-03): проверка ответов на согласие, снимок
// профиля и ключи, документ с уровнем. Идущая сессия назначения
// возвращается она же (FR-AC-03), даже если назначение с тех пор
// отменили: идущая доигрывается (архитектура 9.4).
func (s *service) Start(ctx context.Context, consentIDs []uuid.UUID) (StartResult, error) {
	t, err := participant(ctx)
	if err != nil {
		return StartResult{}, err
	}
	state, err := s.assignmentOf(ctx, t)
	if err != nil {
		return StartResult{}, err
	}
	if row, ok, err := s.store.running(ctx, s.pool, state.AssignmentID); err != nil {
		return StartResult{}, err
	} else if ok {
		return s.resume(ctx, row)
	}
	if err := state.StartError(t.CodeID, s.now()); err != nil {
		return StartResult{}, err
	}
	version, err := s.versions.Version(ctx, state.VersionID)
	if err != nil {
		return StartResult{}, err
	}
	if version.Mode == gen.ModeAssessment {
		past, err := s.store.byAssignments(ctx, s.pool, []uuid.UUID{state.AssignmentID})
		if err != nil {
			return StartResult{}, err
		}
		if len(past) > 0 {
			return StartResult{}, errAssessmentPassed()
		}
	}
	person, err := s.people.Person(ctx, state.SubjectID)
	if errors.Is(err, people.ErrKeyDestroyed) {
		return StartResult{}, errWithdrawn()
	}
	if err != nil {
		return StartResult{}, err
	}

	basis, err := s.consentBasis(ctx, state, version.Mode, consentIDs)
	if err != nil {
		return StartResult{}, err
	}
	externalAI := basis.externalAI != nil && !person.ExternalAIWithdrawn
	if !externalAI {
		basis.externalAI = nil
	}
	snap, err := s.profiles.SnapshotWithKeys(ctx, state.ProfileID, externalAI)
	if err != nil {
		return StartResult{}, err
	}
	if !snap.Routes.OpenRouter && !snap.Routes.OwnServer {
		return StartResult{}, httpx.NewError(httpx.KindNoModelRoute,
			"Без согласия на передачу текста во внешнюю нейросеть разговор провести не получится: "+
				"в вашем профиле тренажёра нет сервера моделей оператора. Обратитесь к HR.")
	}
	doc, parts, err := document(version, state.Difficulty)
	if err != nil {
		return StartResult{}, err
	}
	settingsJSON, err := json.Marshal(snap.Settings)
	if err != nil {
		return StartResult{}, fmt.Errorf("снимок профиля тренажёра: %w", err)
	}

	var id uuid.UUID
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.store.insert(ctx, tx, newSession{
			AssignmentID: state.AssignmentID, SubjectID: state.SubjectID, GroupID: state.GroupID,
			ScenarioID: version.ScenarioID, VersionID: version.ID, Mode: string(version.Mode),
			Difficulty: string(state.Difficulty), ProfileID: snap.ProfileID, ProfileRevision: snap.Revision,
			ProfileSnapshot: settingsJSON, EngineVersion: version.EngineVersion, CriteriaSet: criteriaSet(doc),
			ExternalAIAllowed: externalAI, MainConsentID: basis.main,
			ExternalAIConsentID: basis.externalAI, WrittenConsentID: basis.written,
		})
		return err
	})
	if v, ok := pg.AsViolation(err); ok && v.Kind == pg.Unique {
		switch v.Constraint {
		case "sessions_one_running_per_assignment":
			// Два старта наперегонки: второй получает сессию первого.
			if row, ok, err := s.store.running(ctx, s.pool, state.AssignmentID); err != nil {
				return StartResult{}, err
			} else if ok {
				return s.resume(ctx, row)
			}
		case "sessions_one_assessment_per_assignment":
			return StartResult{}, errAssessmentPassed()
		case "sessions_one_per_consent":
			return StartResult{}, errConsentRequired("Этот ответ на согласие уже использован — подтвердите согласие заново.")
		}
	}
	if err != nil {
		return StartResult{}, fmt.Errorf("запись сессии: %w", err)
	}
	row, err := s.store.session(ctx, s.pool, id)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{
		Created: true, Session: row, Private: parts.Private, Document: doc, Profile: snap,
		OpenRouterKey: snap.OpenRouterKey, ModelServerToken: snap.ModelServerToken,
	}, nil
}

func errAssessmentPassed() *httpx.Error {
	return httpx.NewError(httpx.KindAssessmentPassed,
		"Оценочная попытка по этому назначению уже была — новая попытка только по новому назначению.")
}

// consentIDs — на каких записях согласия основана сессия.
type basisIDs struct {
	main       uuid.UUID
	externalAI *uuid.UUID
	written    *uuid.UUID
}

// consentBasis — разбор consent_ids (arena-api.yaml, trainerStartSession):
// записи этого участника перед этим назначением, не отказы; основной
// ответ — вида, положенного режиму. В оценке ещё и действующая отметка HR
// о письменном согласии (D-51).
func (s *service) consentBasis(ctx context.Context, state assignments.State, mode gen.Mode, ids []uuid.UUID) (basisIDs, error) {
	records, err := s.consents.Records(ctx, ids)
	if err != nil {
		return basisIDs{}, err
	}
	if len(records) != len(uniq(ids)) {
		return basisIDs{}, errConsentRequired("Ответы на согласие не найдены — подтвердите их заново.")
	}
	main := consents.MainKind(mode, false)
	var out basisIDs
	found := false
	for _, r := range records {
		if r.SubjectID != state.SubjectID || r.AssignmentID == nil || *r.AssignmentID != state.AssignmentID {
			return basisIDs{}, errConsentRequired("Ответы на согласие не относятся к этому назначению — подтвердите их заново.")
		}
		if !r.Usable {
			continue
		}
		switch r.Kind {
		case main:
			out.main, found = r.ID, true
		case gen.ConsentKindConsentExternalAi:
			id := r.ID
			out.externalAI = &id
		}
	}
	if !found {
		return basisIDs{}, errConsentRequired("Сессия начинается только после согласия — подтвердите его на экране согласия.")
	}
	if mode == gen.ModeAssessment {
		id, ok, err := s.consents.Usable(ctx, state.SubjectID, nil, gen.ConsentKindWrittenAssessment)
		if err != nil {
			return basisIDs{}, err
		}
		if !ok {
			return basisIDs{}, errConsentRequired("Нет отметки о письменном согласии — обратитесь к HR.")
		}
		out.written = &id
	}
	return out, nil
}

func uniq(ids []uuid.UUID) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// criteriaSet — набор критериев «процесса» из документа.
func criteriaSet(doc json.RawMessage) string {
	var d struct {
		ProcessCriteria string `json:"process_criteria"`
	}
	if err := json.Unmarshal(doc, &d); err != nil || d.ProcessCriteria == "" {
		return defaultCriteriaSet
	}
	return d.ProcessCriteria
}

// Private — повторная выдача после F5 (UC-P-06): только для идущей
// сессии этого участника, согласие повторно не спрашивается.
func (s *service) Private(ctx context.Context, sessionID uuid.UUID) (StartResult, error) {
	t, err := participant(ctx)
	if err != nil {
		return StartResult{}, err
	}
	row, err := s.store.session(ctx, s.pool, sessionID)
	if errors.Is(err, errSessionNotFound) {
		return StartResult{}, errSessionMissing()
	}
	if err != nil {
		return StartResult{}, err
	}
	if row.SubjectID != *t.SubjectID || row.AssignmentID == nil || *row.AssignmentID != *t.AssignmentID {
		return StartResult{}, errSessionMissing()
	}
	if row.Status != string(gen.SessionStatusInProgress) {
		return StartResult{}, httpx.NewError(httpx.KindSessionFinished, "Сессия уже завершена — продолжить её нельзя.")
	}
	return s.resume(ctx, row)
}

// resume — ответ по уже созданной сессии: настройки из снимка, с которым
// она началась (FR-PF-03), ключи — текущие ключи профиля, OpenRouter —
// только если согласие на внешнюю нейросеть было дано и не отозвано.
func (s *service) resume(ctx context.Context, row sessionRow) (StartResult, error) {
	version, err := s.versions.Version(ctx, row.VersionID)
	if err != nil {
		return StartResult{}, err
	}
	person, err := s.people.Person(ctx, row.SubjectID)
	if errors.Is(err, people.ErrKeyDestroyed) {
		return StartResult{}, errWithdrawn()
	}
	if err != nil {
		return StartResult{}, err
	}
	allowed := row.ExternalAIAllowed && !person.ExternalAIWithdrawn
	keys, err := s.profiles.SnapshotWithKeys(ctx, row.ProfileID, allowed)
	if err != nil {
		return StartResult{}, err
	}
	var settings gen.TrainerProfileSettings
	if err := json.Unmarshal(row.ProfileSnapshot, &settings); err != nil {
		return StartResult{}, fmt.Errorf("снимок профиля сессии: %w", err)
	}
	if !allowed {
		settings.Openrouter = nil
	}
	doc, parts, err := document(version, gen.Difficulty(row.Difficulty))
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{
		Session: row, Private: parts.Private, Document: doc,
		Profile:       ProfileSnapshot{ProfileID: row.ProfileID, Revision: row.ProfileRevision, Settings: settings},
		OpenRouterKey: keys.OpenRouterKey, ModelServerToken: keys.ModelServerToken,
	}, nil
}

// --- контракты для соседей ---

// Resolve — consents.AudienceResolver: кто пришёл за экраном согласия
// (D-48). Новые ответы по отменённому назначению или старому коду не
// принимаются — по ним всё равно нельзя начать.
func (s *service) Resolve(ctx context.Context) (consents.Audience, error) {
	t, err := participant(ctx)
	if err != nil {
		return consents.Audience{}, err
	}
	state, err := s.assignmentOf(ctx, t)
	if err != nil {
		return consents.Audience{}, err
	}
	if err := state.StartError(t.CodeID, s.now()); err != nil {
		return consents.Audience{}, err
	}
	briefs, err := s.versions.Briefs(ctx, []uuid.UUID{state.VersionID})
	if err != nil {
		return consents.Audience{}, err
	}
	assignmentID := state.AssignmentID
	return consents.Audience{
		SubjectID: state.SubjectID, AssignmentID: &assignmentID,
		Mode: briefs[state.VersionID].Mode, ProfileID: state.ProfileID,
	}, nil
}

// Summaries — assignments.SessionFacts.
func (s *service) Summaries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]assignments.SessionSummary, error) {
	rows, err := s.store.byAssignments(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]assignments.SessionSummary, len(ids))
	for _, r := range rows {
		sum := out[r.AssignmentID]
		sum.Attempts++
		status := gen.SessionStatus(r.Status)
		switch status {
		case gen.SessionStatusInProgress:
			id := r.ID
			sum.RunningID = &id
		case gen.SessionStatusAbandoned:
		default:
			sum.Finished = true
		}
		started := r.StartedAt
		sum.LastStatus, sum.LastStartedAt = &status, &started
		sum.BreakStage, sum.BreakTurn = r.BreakStage, r.BreakTurn
		sum.Spans = append(sum.Spans, assignments.Span{Start: r.StartedAt, End: r.EndedAt})
		out[r.AssignmentID] = sum
	}
	return out, nil
}

// Counts — scenarios.VersionSessionCounter.
func (s *service) Counts(ctx context.Context, versionIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	return s.store.versionCounts(ctx, s.pool, versionIDs)
}

// RunningByProfile — profiles.SessionCounter.
func (s *service) RunningByProfile(ctx context.Context, profileID uuid.UUID) (int, error) {
	return s.store.runningByProfile(ctx, s.pool, profileID)
}

var (
	_ consents.AudienceResolver       = (*service)(nil)
	_ assignments.SessionFacts        = (*service)(nil)
	_ scenarios.VersionSessionCounter = (*service)(nil)
	_ profiles.SessionCounter         = (*service)(nil)
)

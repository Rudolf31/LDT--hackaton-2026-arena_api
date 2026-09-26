package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var errSessionNotFound = errors.New("сессия не найдена")

// querier — общее у *pgxpool.Pool и pgx.Tx.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// sessionRow — запись сессии: условия, статус, счётчики и столбцы оценок
// (архитектура 8.3).
type sessionRow struct {
	ID                uuid.UUID
	AssignmentID      *uuid.UUID
	SubjectID         uuid.UUID
	GroupID           *uuid.UUID
	ScenarioID        uuid.UUID
	VersionID         uuid.UUID
	Mode              string
	Difficulty        string
	IsDemo            bool
	ProfileID         uuid.UUID
	ProfileRevision   int
	ProfileSnapshot   json.RawMessage
	CriteriaSet       string
	ExternalAIAllowed bool
	Status            string
	StartedAt         time.Time
	EndedAt           *time.Time
	BreakStage        *string
	BreakTurn         *int
	ReopenedCount     int
	ParticipantTurns  int
	Incidents         []byte
	Simplified        bool
	StubUsed          bool
	OpponentViolation int
	FinalID           *string
	FinalTitle        *string
	ResultRank        *string
	ResultNumber      *int
	ResultMax         *int
	ProcessStatus     string
	ProcessNumber     *int
	CriteriaBands     []byte
	ScoringProfile    *string
	Lucky             bool
	Scores            []byte
	JudgeAnswerEnc    []byte
	JudgeAttempts     int
}

const sessionColumns = `id, assignment_id, subject_id, group_id, scenario_id, scenario_version_id, mode, difficulty, is_demo,
	trainer_profile_id, profile_revision, profile_snapshot, criteria_set, external_ai_allowed, status, started_at,
	ended_at, break_stage, break_turn, reopened_count, participant_turns, incidents, simplified, stub_used,
	opponent_violation_count, final_id, final_title, result_rank, result_number, result_max, process_status,
	process_number, criteria_bands, scoring_profile, lucky, scores, judge_answer_enc, judge_attempts`

func scanSession(row pgx.Row) (sessionRow, error) {
	var r sessionRow
	err := row.Scan(&r.ID, &r.AssignmentID, &r.SubjectID, &r.GroupID, &r.ScenarioID, &r.VersionID, &r.Mode, &r.Difficulty,
		&r.IsDemo, &r.ProfileID, &r.ProfileRevision, &r.ProfileSnapshot, &r.CriteriaSet, &r.ExternalAIAllowed, &r.Status,
		&r.StartedAt, &r.EndedAt, &r.BreakStage, &r.BreakTurn, &r.ReopenedCount, &r.ParticipantTurns, &r.Incidents,
		&r.Simplified, &r.StubUsed, &r.OpponentViolation, &r.FinalID, &r.FinalTitle, &r.ResultRank, &r.ResultNumber,
		&r.ResultMax, &r.ProcessStatus, &r.ProcessNumber, &r.CriteriaBands, &r.ScoringProfile, &r.Lucky, &r.Scores,
		&r.JudgeAnswerEnc, &r.JudgeAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionRow{}, errSessionNotFound
	}
	if err != nil {
		return sessionRow{}, fmt.Errorf("чтение сессии: %w", err)
	}
	return r, nil
}

type store struct{}

func newStore() *store { return &store{} }

func (s *store) session(ctx context.Context, q querier, id uuid.UUID) (sessionRow, error) {
	return scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, id))
}

// running — идущая сессия назначения; их не бывает больше одной
// (sessions_one_running_per_assignment, FR-AC-03).
func (s *store) running(ctx context.Context, q querier, assignmentID uuid.UUID) (sessionRow, bool, error) {
	r, err := scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+`
		FROM sessions WHERE assignment_id = $1 AND status = 'in_progress'`, assignmentID))
	if errors.Is(err, errSessionNotFound) {
		return sessionRow{}, false, nil
	}
	return r, err == nil, err
}

type newSession struct {
	AssignmentID        uuid.UUID
	SubjectID           uuid.UUID
	GroupID             uuid.UUID
	ScenarioID          uuid.UUID
	VersionID           uuid.UUID
	Mode                string
	Difficulty          string
	ProfileID           uuid.UUID
	ProfileRevision     int
	ProfileSnapshot     []byte
	EngineVersion       string
	CriteriaSet         string
	ExternalAIAllowed   bool
	MainConsentID       uuid.UUID
	ExternalAIConsentID *uuid.UUID
	WrittenConsentID    *uuid.UUID
}

// insert — новая сессия. Ошибки уникальности (идущая по назначению,
// оценка уже была, ответ на согласие использован) разбирает служба.
func (s *store) insert(ctx context.Context, tx pgx.Tx, n newSession) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO sessions (assignment_id, subject_id, group_id, scenario_id, scenario_version_id, mode, difficulty,
			trainer_profile_id, profile_revision, profile_snapshot, engine_version, criteria_set,
			external_ai_allowed, main_consent_id, external_ai_consent_id, written_consent_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id`,
		n.AssignmentID, n.SubjectID, n.GroupID, n.ScenarioID, n.VersionID, n.Mode, n.Difficulty,
		n.ProfileID, n.ProfileRevision, n.ProfileSnapshot, n.EngineVersion, n.CriteriaSet,
		n.ExternalAIAllowed, n.MainConsentID, n.ExternalAIConsentID, n.WrittenConsentID).Scan(&id)
	return id, err
}

// assignmentSession — одна сессия назначения для сводки.
type assignmentSession struct {
	AssignmentID uuid.UUID
	ID           uuid.UUID
	Status       string
	StartedAt    time.Time
	EndedAt      *time.Time
	BreakStage   *string
	BreakTurn    *int
}

func (s *store) byAssignments(ctx context.Context, q querier, ids []uuid.UUID) ([]assignmentSession, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT assignment_id, id, status, started_at, ended_at, break_stage, break_turn
		FROM sessions WHERE assignment_id = ANY($1)
		ORDER BY started_at, id`, ids)
	if err != nil {
		return nil, fmt.Errorf("сессии назначений: %w", err)
	}
	defer rows.Close()
	var out []assignmentSession
	for rows.Next() {
		var r assignmentSession
		if err := rows.Scan(&r.AssignmentID, &r.ID, &r.Status, &r.StartedAt, &r.EndedAt, &r.BreakStage, &r.BreakTurn); err != nil {
			return nil, fmt.Errorf("сессии назначений: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("сессии назначений: %w", err)
	}
	return out, nil
}

// versionCounts — сессии на версиях сценария, без демо (FR-AC-10).
func (s *store) versionCounts(ctx context.Context, q querier, versionIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(versionIDs))
	if len(versionIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT scenario_version_id, count(*) FROM sessions
		WHERE scenario_version_id = ANY($1) AND NOT is_demo
		GROUP BY scenario_version_id`, versionIDs)
	if err != nil {
		return nil, fmt.Errorf("число сессий на версиях: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("число сессий на версиях: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("число сессий на версиях: %w", err)
	}
	return out, nil
}

func (s *store) runningByProfile(ctx context.Context, q querier, profileID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE trainer_profile_id = $1 AND status = 'in_progress'`,
		profileID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("идущие сессии по профилю: %w", err)
	}
	return n, nil
}

// --- разговор (этап 08) ---

// lock — строка сессии под блокировкой до конца транзакции: пачки
// событий, завершение и ответ судьи по одной сессии идут друг за другом.
func (s *store) lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (sessionRow, error) {
	return scanSession(tx.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1 FOR UPDATE`, id))
}

// turnRow — строка turns; тексты уже зашифрованы (или nil, если ключ
// участника уничтожен).
type turnRow struct {
	Seq               int
	Speaker           string
	ReplyNo           int
	TextEnc           []byte
	AtMs              int
	DurationMs        *int
	PauseMs           *int
	ModelRoute        *string
	Stage             string
	TransitionTo      *string
	MoveType          *string
	MoveSource        *string
	JudgeConfidence   *float64
	Evidence          *string
	Interest          *string
	Violations        []string
	Judge             []byte
	Terms             []byte
	Trust             *int
	Pressure          *int
	Credibility       *int
	Patience          *int
	Credit            *int
	RevealedFacts     []string
	EngineStep        []byte
	Intent            *string
	Offer             []byte
	OpponentJudge     []byte
	OpponentViolation bool
	Interrupted       bool
	CommentEnc        []byte
}

// insertTurns — пачка реплик одним обменом с базой. Повтор по
// (session_id, seq) отбрасывает первичный ключ (FR-AC-11); вернёт число
// принятых строк. Прочие нарушения (turns_reply_key, CHECK) — ошибка,
// её разбирает служба.
func (s *store) insertTurns(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, turns []turnRow) (int, error) {
	if len(turns) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, t := range turns {
		batch.Queue(`
			INSERT INTO turns (session_id, seq, speaker, reply_no, text_enc, at_ms, duration_ms, pause_ms, model_route,
				stage, transition_to, move_type, move_source, judge_confidence, evidence, interest, violations, judge,
				terms, trust, pressure, credibility, patience, credit, revealed_facts, engine_step, intent, offer,
				opponent_judge, opponent_violation, interrupted, opponent_judge_comment_enc)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21,
				$22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32)
			ON CONFLICT (session_id, seq) DO NOTHING`,
			sessionID, t.Seq, t.Speaker, t.ReplyNo, t.TextEnc, t.AtMs, t.DurationMs, t.PauseMs, t.ModelRoute,
			t.Stage, t.TransitionTo, t.MoveType, t.MoveSource, t.JudgeConfidence, t.Evidence, t.Interest,
			nonNil(t.Violations), jsonOrNil(t.Judge), jsonOrNil(t.Terms), t.Trust, t.Pressure, t.Credibility,
			t.Patience, t.Credit, nonNil(t.RevealedFacts), jsonOrNil(t.EngineStep), t.Intent, jsonOrNil(t.Offer),
			jsonOrNil(t.OpponentJudge), t.OpponentViolation, t.Interrupted, t.CommentEnc)
	}
	results := tx.SendBatch(ctx, batch)
	accepted := 0
	for range turns {
		tag, err := results.Exec()
		if err != nil {
			return 0, errors.Join(err, results.Close())
		}
		accepted += int(tag.RowsAffected())
	}
	if err := results.Close(); err != nil {
		return 0, err
	}
	return accepted, nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// jsonOrNil — jsonb-параметр: nil уходит как SQL NULL, а не как пустая
// строка, которую PostgreSQL не примет за JSON.
func jsonOrNil(raw []byte) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

// seqs — номера реплик сессии по порядку: для пропусков и missing_turns.
func (s *store) seqs(ctx context.Context, q querier, sessionID uuid.UUID) ([]int, error) {
	rows, err := q.Query(ctx, `SELECT seq FROM turns WHERE session_id = $1 ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("номера реплик сессии: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, fmt.Errorf("номера реплик сессии: %w", err)
	}
	return out, nil
}

// turns — все реплики сессии по порядку seq.
func (s *store) turns(ctx context.Context, q querier, sessionID uuid.UUID) ([]turnRow, error) {
	rows, err := q.Query(ctx, `
		SELECT seq, speaker, reply_no, text_enc, at_ms, duration_ms, pause_ms, model_route, stage, transition_to,
			move_type, move_source, judge_confidence, evidence, interest, violations, judge, terms, trust, pressure,
			credibility, patience, credit, revealed_facts, engine_step, intent, offer, opponent_judge,
			opponent_violation, interrupted, opponent_judge_comment_enc
		FROM turns WHERE session_id = $1 ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("реплики сессии: %w", err)
	}
	defer rows.Close()
	var out []turnRow
	for rows.Next() {
		var t turnRow
		var confidence *float32
		if err := rows.Scan(&t.Seq, &t.Speaker, &t.ReplyNo, &t.TextEnc, &t.AtMs, &t.DurationMs, &t.PauseMs,
			&t.ModelRoute, &t.Stage, &t.TransitionTo, &t.MoveType, &t.MoveSource, &confidence, &t.Evidence,
			&t.Interest, &t.Violations, &t.Judge, &t.Terms, &t.Trust, &t.Pressure, &t.Credibility, &t.Patience,
			&t.Credit, &t.RevealedFacts, &t.EngineStep, &t.Intent, &t.Offer, &t.OpponentJudge,
			&t.OpponentViolation, &t.Interrupted, &t.CommentEnc); err != nil {
			return nil, fmt.Errorf("реплики сессии: %w", err)
		}
		if confidence != nil {
			c := float64(*confidence)
			t.JudgeConfidence = &c
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("реплики сессии: %w", err)
	}
	return out, nil
}

// afterEvents — счётчики и пометки сессии после пачки (архитектура 7.2,
// шаги 2.4–2.5). Счётчики пересчитываются по turns, поэтому повторная
// доставка их не раздувает. Упрощённый режим снимает «просмотрено» тем же
// UPDATE: упрощённая сессия не годится для решения (NFR-R-02).
func (s *store) afterEvents(ctx context.Context, tx pgx.Tx, id uuid.UUID, simplified, stub bool, incidents []byte) error {
	_, err := tx.Exec(ctx, `
		UPDATE sessions SET
			last_event_at = now(),
			participant_turns = (SELECT count(*) FROM turns WHERE session_id = $1 AND speaker = 'participant'),
			opponent_violation_count = (SELECT count(*) FROM turns WHERE session_id = $1 AND opponent_violation),
			simplified = simplified OR $2,
			stub_used = stub_used OR $3,
			incidents = $4,
			reviewed_at = CASE WHEN $2 THEN NULL ELSE reviewed_at END,
			reviewed_by = CASE WHEN $2 THEN NULL ELSE reviewed_by END,
			reviewed_violation_count = CASE WHEN $2 THEN NULL ELSE reviewed_violation_count END
		WHERE id = $1`, id, simplified, stub, string(incidents))
	if err != nil {
		return fmt.Errorf("счётчики сессии: %w", err)
	}
	return nil
}

// reopen — прерванная сервером сессия снова идёт (архитектура 7.2, шаг
// 2.6). Если по назначению уже идёт другая, вернётся нарушение
// sessions_one_running_per_assignment — его разбирает служба.
func (s *store) reopen(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		UPDATE sessions SET status = 'in_progress', ended_at = NULL, process_status = 'pending',
			break_stage = NULL, break_turn = NULL, reopened_count = reopened_count + 1
		WHERE id = $1 RETURNING reopened_count`, id).Scan(&n)
	return n, err
}

// lastStage — этап после последней реплики; nil — реплик нет.
func (s *store) lastStage(ctx context.Context, q querier, id uuid.UUID) (*string, error) {
	var stage string
	err := q.QueryRow(ctx, `SELECT stage FROM turns WHERE session_id = $1 ORDER BY seq DESC LIMIT 1`, id).Scan(&stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("последний этап сессии: %w", err)
	}
	return &stage, nil
}

// grade — итог сессии и оценки по столбцам (архитектура 8.3).
type grade struct {
	Status         string
	BreakStage     *string
	BreakTurn      *int
	FinalID        *string
	FinalTitle     *string
	ResultRank     *string
	ResultNumber   *int
	ResultMax      *int
	ProcessStatus  string
	ProcessNumber  *int
	CriteriaBands  []byte
	ScoringProfile string
	Lucky          bool
	Scores         []byte
	JudgeAnswerEnc []byte
	JudgeAttempts  int
}

func (s *store) finish(ctx context.Context, tx pgx.Tx, id uuid.UUID, g grade) error {
	_, err := tx.Exec(ctx, `
		UPDATE sessions SET status = $2, ended_at = now(), break_stage = $3, break_turn = $4, final_id = $5,
			final_title = $6, result_rank = $7, result_number = $8, result_max = $9, process_status = $10,
			process_number = $11, criteria_bands = $12, scoring_profile = $13, lucky = $14, scores = $15,
			scored_at = now(), judge_answer_enc = $16, judge_attempts = $17
		WHERE id = $1`,
		id, g.Status, g.BreakStage, g.BreakTurn, g.FinalID, g.FinalTitle, g.ResultRank, g.ResultNumber, g.ResultMax,
		g.ProcessStatus, g.ProcessNumber, jsonOrNil(g.CriteriaBands), g.ScoringProfile, g.Lucky, jsonOrNil(g.Scores),
		g.JudgeAnswerEnc, g.JudgeAttempts)
	return err
}

// processAnswered — «процесс» получен повторным запросом к судье
// (arena-scoring 8.3): меняются только его столбцы, блок и ответ судьи.
func (s *store) processAnswered(ctx context.Context, tx pgx.Tx, id uuid.UUID, g grade) error {
	_, err := tx.Exec(ctx, `
		UPDATE sessions SET process_status = 'ok', process_number = $2, criteria_bands = $3, lucky = $4,
			scores = $5, scored_at = now(), judge_answer_enc = $6, judge_attempts = $7
		WHERE id = $1`,
		id, g.ProcessNumber, jsonOrNil(g.CriteriaBands), g.Lucky, jsonOrNil(g.Scores), g.JudgeAnswerEnc, g.JudgeAttempts)
	return err
}

// closedSession — сессия, закрытая заданием по таймауту.
type closedSession struct {
	ID        uuid.UUID
	SubjectID uuid.UUID
	GroupID   *uuid.UUID
}

// closeAbandoned — запрос архитектуры 7.3. Таймаут считается от более
// позднего из «последнее событие» и «портал запущен»: время
// недоступности портала в него не входит (FR-ST-03, I-15).
func (s *store) closeAbandoned(ctx context.Context, tx pgx.Tx, portalStartedAt time.Time, timeoutMinutes int) ([]closedSession, error) {
	rows, err := tx.Query(ctx, `
		UPDATE sessions s
		   SET status = 'abandoned', ended_at = now(), process_status = 'not_scored',
		       break_turn = s.participant_turns,
		       break_stage = (SELECT t.stage FROM turns t
		                      WHERE t.session_id = s.id ORDER BY t.seq DESC LIMIT 1)
		 WHERE s.status = 'in_progress'
		   AND greatest(s.last_event_at, $1::timestamptz) < now() - make_interval(mins => $2::int)
		RETURNING s.id, s.subject_id, s.group_id`, portalStartedAt, timeoutMinutes)
	if err != nil {
		return nil, fmt.Errorf("закрытие брошенных сессий: %w", err)
	}
	defer rows.Close()
	var out []closedSession
	for rows.Next() {
		var c closedSession
		if err := rows.Scan(&c.ID, &c.SubjectID, &c.GroupID); err != nil {
			return nil, fmt.Errorf("закрытие брошенных сессий: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("закрытие брошенных сессий: %w", err)
	}
	return out, nil
}

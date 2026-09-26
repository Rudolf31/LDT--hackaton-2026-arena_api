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

// sessionRow — то, что нужно для входа, старта и повторной выдачи.
type sessionRow struct {
	ID                uuid.UUID
	AssignmentID      *uuid.UUID
	SubjectID         uuid.UUID
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
}

const sessionColumns = `id, assignment_id, subject_id, scenario_version_id, mode, difficulty, is_demo,
	trainer_profile_id, profile_revision, profile_snapshot, criteria_set, external_ai_allowed, status, started_at`

func scanSession(row pgx.Row) (sessionRow, error) {
	var r sessionRow
	err := row.Scan(&r.ID, &r.AssignmentID, &r.SubjectID, &r.VersionID, &r.Mode, &r.Difficulty, &r.IsDemo,
		&r.ProfileID, &r.ProfileRevision, &r.ProfileSnapshot, &r.CriteriaSet, &r.ExternalAIAllowed, &r.Status, &r.StartedAt)
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

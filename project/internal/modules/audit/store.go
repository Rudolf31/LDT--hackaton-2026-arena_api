package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/platform/httpx"
)

// forbiddenDetailKeys — то, что в details не попадает никогда (CLAUDE.md,
// правило 5): имена, псевдонимы и тексты реплик. Список не исчерпывающий —
// это последний защитный рубеж, а не замена ревью.
var forbiddenDetailKeys = []string{"full_name", "pseudonym", "text", "message", "comment", "transcript"}

type store struct{}

func newStore() *store {
	return &store{}
}

func (s *store) insert(ctx context.Context, tx pgx.Tx, e Entry) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}

	if pointer, found := httpx.FindEmotionKey(details); found {
		return fmt.Errorf("запись в журнал содержит запрещённый ключ emotion (%s) — не пишем (CLAUDE.md, правило 1)", pointer)
	}
	if key, found := findAnyKey(details, forbiddenDetailKeys); found {
		return fmt.Errorf("запись в журнал содержит запрещённое поле %q — имена и тексты реплик в журнал не пишем (CLAUDE.md, правило 5)", key)
	}

	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("сериализация details журнала: %w", err)
	}

	outcome := e.Outcome
	if outcome == "" {
		outcome = OutcomeOK
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log (actor_kind, actor_user_id, action, outcome, subject_id, session_id, group_id, rows_count, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, string(e.ActorKind), e.ActorUserID, string(e.Action), string(outcome), e.SubjectID, e.SessionID, e.GroupID, e.RowsCount, detailsJSON)
	if err != nil {
		return fmt.Errorf("запись в журнал: %w", err)
	}
	return nil
}

// findAnyKey ищет любой из names в details на любой глубине (карты и
// списки) — details это jsonb произвольной формы, а не плоский набор пар.
func findAnyKey(v any, names []string) (string, bool) {
	switch node := v.(type) {
	case map[string]any:
		for _, name := range names {
			if _, ok := node[name]; ok {
				return name, true
			}
		}
		for _, child := range node {
			if key, found := findAnyKey(child, names); found {
				return key, true
			}
		}
	case []any:
		for _, child := range node {
			if key, found := findAnyKey(child, names); found {
				return key, true
			}
		}
	}
	return "", false
}

type row struct {
	ID          uuid.UUID
	OccurredAt  time.Time
	ActorKind   string
	ActorUserID *uuid.UUID
	Action      string
	Outcome     string
	SubjectID   *uuid.UUID
	SessionID   *uuid.UUID
	GroupID     *uuid.UUID
	RowsCount   *int
	Details     map[string]any
}

type filter struct {
	From        *time.Time
	To          *time.Time
	ActorUserID *uuid.UUID
	GroupID     *uuid.UUID
	SubjectID   *uuid.UUID
	Action      *string
	Outcome     *string
	// Limit 0 — без ограничения (выгрузка).
	Limit  int
	Offset int
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// list — строки журнала, новые сверху, и общее число подходящих.
func (s *store) list(ctx context.Context, q querier, f filter) ([]row, int, error) {
	var limit *int
	if f.Limit > 0 {
		limit = &f.Limit
	}
	rows, err := q.Query(ctx, `
		SELECT id, occurred_at, actor_kind, actor_user_id, action, outcome, subject_id, session_id,
		       group_id, rows_count, details, count(*) OVER ()
		FROM audit_log
		WHERE ($1::timestamptz IS NULL OR occurred_at >= $1)
		  AND ($2::timestamptz IS NULL OR occurred_at < $2)
		  AND ($3::uuid IS NULL OR actor_user_id = $3)
		  AND ($4::uuid IS NULL OR group_id = $4)
		  AND ($5::uuid IS NULL OR subject_id = $5)
		  AND ($6::text IS NULL OR action::text = $6)
		  AND ($7::text IS NULL OR outcome = $7)
		ORDER BY occurred_at DESC, id
		LIMIT $8 OFFSET $9
	`, f.From, f.To, f.ActorUserID, f.GroupID, f.SubjectID, f.Action, f.Outcome, limit, f.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("чтение журнала: %w", err)
	}
	defer rows.Close()

	var out []row
	total := 0
	for rows.Next() {
		var r row
		var details []byte
		if err := rows.Scan(&r.ID, &r.OccurredAt, &r.ActorKind, &r.ActorUserID, &r.Action, &r.Outcome,
			&r.SubjectID, &r.SessionID, &r.GroupID, &r.RowsCount, &details, &total); err != nil {
			return nil, 0, fmt.Errorf("чтение журнала: %w", err)
		}
		if err := json.Unmarshal(details, &r.Details); err != nil {
			return nil, 0, fmt.Errorf("разбор details журнала: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("чтение журнала: %w", err)
	}
	return out, total, nil
}

package settings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/platform/pg"
)

// RangeError — значение не прошло CHECK-ограничение portal_settings.
// Схема запроса (arena-api.yaml: PortalSettingsPatch) держит те же
// границы, так что дойти до этой ошибки в обычной работе не должно —
// это защита на случай расхождения схемы и ограничения, а не обычный путь.
type RangeError struct {
	Constraint string
}

func (e *RangeError) Error() string {
	return fmt.Sprintf("значение вне допустимого диапазона (ограничение %s)", e.Constraint)
}

// querier — то общее, что есть у *pgxpool.Pool и pgx.Tx: store не решает,
// идёт ли речь о транзакции — это решает service.go.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type row struct {
	AdmissionRehearsals   int
	AbandonTimeoutMinutes int
	CodeMaxFailedAttempts int
	UpdatedAt             time.Time
	UpdatedByName         *string
}

type store struct{}

func newStore() *store {
	return &store{}
}

func (s *store) get(ctx context.Context, q querier) (row, error) {
	var r row
	err := q.QueryRow(ctx, `
		SELECT ps.admission_rehearsals, ps.abandon_timeout_minutes, ps.code_max_failed_attempts,
		       ps.updated_at, u.full_name
		FROM portal_settings ps
		LEFT JOIN portal_users u ON u.id = ps.updated_by
		WHERE ps.id
	`).Scan(&r.AdmissionRehearsals, &r.AbandonTimeoutMinutes, &r.CodeMaxFailedAttempts, &r.UpdatedAt, &r.UpdatedByName)
	if err != nil {
		return row{}, fmt.Errorf("чтение настроек портала: %w", err)
	}
	return r, nil
}

func (s *store) update(ctx context.Context, tx pgx.Tx, patch Patch, actorUserID *uuid.UUID) (row, error) {
	var r row
	err := tx.QueryRow(ctx, `
		UPDATE portal_settings SET
			admission_rehearsals     = COALESCE($1, admission_rehearsals),
			abandon_timeout_minutes  = COALESCE($2, abandon_timeout_minutes),
			code_max_failed_attempts = COALESCE($3, code_max_failed_attempts),
			updated_at = now(),
			updated_by = $4
		WHERE id
		RETURNING admission_rehearsals, abandon_timeout_minutes, code_max_failed_attempts, updated_at
	`, patch.AdmissionRehearsals, patch.AbandonTimeoutMinutes, patch.CodeMaxFailedAttempts, actorUserID).
		Scan(&r.AdmissionRehearsals, &r.AbandonTimeoutMinutes, &r.CodeMaxFailedAttempts, &r.UpdatedAt)
	if err != nil {
		if violation, ok := pg.AsViolation(err); ok && violation.Kind == pg.Check {
			return row{}, &RangeError{Constraint: violation.Constraint}
		}
		return row{}, fmt.Errorf("сохранение настроек портала: %w", err)
	}

	// Имя автора изменения читаем отдельным запросом той же транзакцией —
	// RETURNING не умеет присоединять чужую таблицу.
	if actorUserID != nil {
		if err := tx.QueryRow(ctx, `SELECT full_name FROM portal_users WHERE id = $1`, *actorUserID).Scan(&r.UpdatedByName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return row{}, fmt.Errorf("чтение имени автора изменения настроек: %w", err)
		}
	}

	return r, nil
}

package assignments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var errAssignmentNotFound = errors.New("назначение не найдено")

// querier — общее у *pgxpool.Pool и pgx.Tx.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// assignmentRow — назначение с его действующим кодом (если он есть) и
// именем того, кто назначил: HR — сотрудник портала, не участник; имя
// читается join'ом, как в settings и scenarios (D-30).
type assignmentRow struct {
	ID            uuid.UUID
	SubjectID     uuid.UUID
	GroupID       uuid.UUID
	VersionID     uuid.UUID
	Difficulty    string
	ProfileID     uuid.UUID
	DueAt         time.Time
	BatchID       *uuid.UUID
	CreatedAt     time.Time
	CreatedByName *string
	CancelledAt   *time.Time
	CancelReason  *string
	CodeID        *uuid.UUID
	CodeIssuedAt  *time.Time
	CodeFailed    *int
	CodeBlockedAt *time.Time
}

const assignmentColumns = `a.id, a.subject_id, a.group_id, a.scenario_version_id, a.difficulty, a.trainer_profile_id,
	a.due_at, a.batch_id, a.created_at, u.full_name, a.cancelled_at, a.cancel_reason,
	c.id, c.issued_at, c.failed_attempts, c.blocked_at`

const assignmentFrom = ` FROM assignments a
	LEFT JOIN portal_users u ON u.id = a.created_by
	LEFT JOIN access_codes c ON c.assignment_id = a.id AND c.revoked_at IS NULL`

func scanAssignment(row pgx.Row) (assignmentRow, error) {
	var r assignmentRow
	err := row.Scan(&r.ID, &r.SubjectID, &r.GroupID, &r.VersionID, &r.Difficulty, &r.ProfileID,
		&r.DueAt, &r.BatchID, &r.CreatedAt, &r.CreatedByName, &r.CancelledAt, &r.CancelReason,
		&r.CodeID, &r.CodeIssuedAt, &r.CodeFailed, &r.CodeBlockedAt)
	return r, err
}

type store struct{}

func newStore() *store { return &store{} }

func (s *store) assignment(ctx context.Context, q querier, id uuid.UUID, forUpdate bool) (assignmentRow, error) {
	sql := `SELECT ` + assignmentColumns + assignmentFrom + ` WHERE a.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF a`
	}
	r, err := scanAssignment(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return assignmentRow{}, errAssignmentNotFound
	}
	if err != nil {
		return assignmentRow{}, fmt.Errorf("чтение назначения: %w", err)
	}
	return r, nil
}

// listFilter — фильтры списка. GroupIDs — группы, в пределах которых
// ищем (доступные пользователю); пустой срез — ни одной. Limit 0 — без
// страницы: нужен, когда статус фильтруется в памяти (D-52).
type listFilter struct {
	GroupIDs   []uuid.UUID
	ID         *uuid.UUID
	SubjectID  *uuid.UUID
	VersionIDs []uuid.UUID // nil — без фильтра по сценарию
	BatchID    *uuid.UUID
	DueBefore  *time.Time
	Limit      int
	Offset     int
}

func (s *store) list(ctx context.Context, q querier, f listFilter) ([]assignmentRow, int, error) {
	where := []string{"a.group_id = ANY($1)"}
	args := []any{f.GroupIDs}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "$?", fmt.Sprintf("$%d", len(args))))
	}
	if f.ID != nil {
		add("a.id = $?", *f.ID)
	}
	if f.SubjectID != nil {
		add("a.subject_id = $?", *f.SubjectID)
	}
	if f.VersionIDs != nil {
		add("a.scenario_version_id = ANY($?)", f.VersionIDs)
	}
	if f.BatchID != nil {
		add("a.batch_id = $?", *f.BatchID)
	}
	if f.DueBefore != nil {
		add("a.due_at < $?", *f.DueBefore)
	}
	cond := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM assignments a`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("список назначений: %w", err)
	}
	sql := `SELECT ` + assignmentColumns + assignmentFrom + cond + ` ORDER BY a.created_at DESC, a.id`
	if f.Limit > 0 {
		sql += fmt.Sprintf(` LIMIT %d OFFSET %d`, f.Limit, f.Offset)
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("список назначений: %w", err)
	}
	defer rows.Close()
	var out []assignmentRow
	for rows.Next() {
		r, err := scanAssignment(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("список назначений: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("список назначений: %w", err)
	}
	return out, total, nil
}

type newAssignment struct {
	SubjectID  uuid.UUID
	GroupID    uuid.UUID
	VersionID  uuid.UUID
	Difficulty string
	ProfileID  uuid.UUID
	DueAt      time.Time
	BatchID    *uuid.UUID
	CreatedBy  uuid.UUID
}

func (s *store) insertAssignment(ctx context.Context, tx pgx.Tx, a newAssignment) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO assignments (subject_id, group_id, scenario_version_id, difficulty, trainer_profile_id, due_at, batch_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		a.SubjectID, a.GroupID, a.VersionID, a.Difficulty, a.ProfileID, a.DueAt, a.BatchID, a.CreatedBy).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("запись назначения: %w", err)
	}
	return id, nil
}

// insertCode — новая строка кода. Ошибку уникальности селектора
// вызывающий разбирает сам: она означает «выпустить код заново» (6.3).
func (s *store) insertCode(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID, selector, full []byte, issuedBy *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO access_codes (assignment_id, selector_hash, code_hash, issued_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, assignmentID, selector, full, issuedBy).Scan(&id)
	return id, err
}

// revokeActive — отзыв действующего кода назначения; false — его не было.
func (s *store) revokeActive(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID, reason string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE access_codes SET revoked_at = now(), revoke_reason = $2
		WHERE assignment_id = $1 AND revoked_at IS NULL`, assignmentID, reason)
	if err != nil {
		return false, fmt.Errorf("отзыв кода: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// codeRow — код, найденный при вводе.
type codeRow struct {
	ID             uuid.UUID
	AssignmentID   uuid.UUID
	FailedAttempts int
	BlockedAt      *time.Time
	RevokedAt      *time.Time
	RevokeReason   *string
}

const codeColumns = `id, assignment_id, failed_attempts, blocked_at, revoked_at, revoke_reason`

func scanCode(row pgx.Row) (codeRow, bool, error) {
	var c codeRow
	err := row.Scan(&c.ID, &c.AssignmentID, &c.FailedAttempts, &c.BlockedAt, &c.RevokedAt, &c.RevokeReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return codeRow{}, false, nil
	}
	if err != nil {
		return codeRow{}, false, fmt.Errorf("чтение кода доступа: %w", err)
	}
	return c, true, nil
}

func (s *store) codeByHash(ctx context.Context, tx pgx.Tx, full []byte) (codeRow, bool, error) {
	return scanCode(tx.QueryRow(ctx, `SELECT `+codeColumns+` FROM access_codes WHERE code_hash = $1 FOR UPDATE`, full))
}

func (s *store) activeCodeBySelector(ctx context.Context, tx pgx.Tx, selector []byte) (codeRow, bool, error) {
	return scanCode(tx.QueryRow(ctx, `SELECT `+codeColumns+` FROM access_codes
		WHERE selector_hash = $1 AND revoked_at IS NULL FOR UPDATE`, selector))
}

// failAttempt — неудачная попытка по коду; block — заодно заблокировать.
func (s *store) failAttempt(ctx context.Context, tx pgx.Tx, codeID uuid.UUID, block bool) error {
	_, err := tx.Exec(ctx, `
		UPDATE access_codes
		SET failed_attempts = failed_attempts + 1, last_failed_at = now(),
		    blocked_at = CASE WHEN $2 THEN COALESCE(blocked_at, now()) ELSE blocked_at END
		WHERE id = $1`, codeID, block)
	if err != nil {
		return fmt.Errorf("учёт неудачной попытки кода: %w", err)
	}
	return nil
}

func (s *store) setDue(ctx context.Context, tx pgx.Tx, id uuid.UUID, due time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE assignments SET due_at = $2 WHERE id = $1`, id, due); err != nil {
		return fmt.Errorf("продление назначения: %w", err)
	}
	return nil
}

func (s *store) cancel(ctx context.Context, tx pgx.Tx, id uuid.UUID, by uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE assignments SET cancelled_at = now(), cancelled_by = $2, cancel_reason = 'by_hr'
		WHERE id = $1 AND cancelled_at IS NULL`, id, by)
	if err != nil {
		return fmt.Errorf("отмена назначения: %w", err)
	}
	return nil
}

func (s *store) unblock(ctx context.Context, tx pgx.Tx, codeID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE access_codes SET failed_attempts = 0, blocked_at = NULL WHERE id = $1`, codeID)
	if err != nil {
		return fmt.Errorf("снятие блокировки кода: %w", err)
	}
	return nil
}

// cancelForSubject — отзыв согласия (архитектура 9.3): открытые
// назначения участника отменяются, их коды отзываются.
func (s *store) cancelForSubject(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) (int, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE assignments SET cancelled_at = now(), cancel_reason = 'consent_withdrawn'
		WHERE subject_id = $1 AND cancelled_at IS NULL`, subjectID)
	if err != nil {
		return 0, fmt.Errorf("отмена назначений при отзыве согласия: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE access_codes SET revoked_at = now(), revoke_reason = 'consent_withdrawn'
		WHERE revoked_at IS NULL
		  AND assignment_id IN (SELECT id FROM assignments WHERE subject_id = $1)`, subjectID); err != nil {
		return 0, fmt.Errorf("отзыв кодов при отзыве согласия: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

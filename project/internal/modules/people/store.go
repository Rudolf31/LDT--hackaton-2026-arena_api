package people

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/platform/pg"
)

var (
	errGroupNotFound       = errors.New("группа не найдена")
	errGroupNameTaken      = errors.New("название группы занято")
	errProfileNotFound     = errors.New("профиль тренажёра не найден")
	errPersonNotFound      = errors.New("сотрудник не найден")
	errPersonnelNoTaken    = errors.New("табельный номер занят")
	errSubjectNumberTaken  = errors.New("номер участника занят")
	errPersonNeedsNameForm = errors.New("нужно ФИО или псевдоним")
)

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type groupRow struct {
	ID               uuid.UUID
	Name             string
	Department       *string
	TrainerProfileID *uuid.UUID
	ArchivedAt       *time.Time
	PeopleCount      int
}

type personRow struct {
	SubjectID             uuid.UUID
	Number                string
	GroupID               uuid.UUID
	GroupName             string
	FullName              *string
	Pseudonym             *string
	PersonnelNo           *string
	JobTitle              *string
	ExternalAIWithdrawnAt *time.Time
}

type store struct{}

func newStore() *store { return &store{} }

// --- группы ---

const groupColumns = `g.id, g.name, g.department, g.trainer_profile_id, g.archived_at,
	(SELECT count(*) FROM people p WHERE p.group_id = g.id)`

func scanGroup(row pgx.Row) (groupRow, error) {
	var r groupRow
	err := row.Scan(&r.ID, &r.Name, &r.Department, &r.TrainerProfileID, &r.ArchivedAt, &r.PeopleCount)
	return r, err
}

func (s *store) listGroups(ctx context.Context, q querier, includeArchived bool) ([]groupRow, error) {
	rows, err := q.Query(ctx, `SELECT `+groupColumns+` FROM employee_groups g
		WHERE $1 OR g.archived_at IS NULL ORDER BY g.name`, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("список групп: %w", err)
	}
	defer rows.Close()
	var out []groupRow
	for rows.Next() {
		r, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("список групп: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("список групп: %w", err)
	}
	return out, nil
}

func (s *store) group(ctx context.Context, q querier, id uuid.UUID) (groupRow, error) {
	r, err := scanGroup(q.QueryRow(ctx, `SELECT `+groupColumns+` FROM employee_groups g WHERE g.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return groupRow{}, errGroupNotFound
	}
	if err != nil {
		return groupRow{}, fmt.Errorf("чтение группы: %w", err)
	}
	return r, nil
}

func (s *store) insertGroup(ctx context.Context, tx pgx.Tx, g NewGroup, profileID *uuid.UUID, createdBy *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO employee_groups (name, department, trainer_profile_id, created_by)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, g.Name, g.Department, profileID, createdBy).Scan(&id)
	if err != nil {
		return uuid.Nil, groupWriteError(err, "заведение группы")
	}
	return id, nil
}

type groupUpdate struct {
	Name             *string
	Department       *string
	TrainerProfileID *uuid.UUID
	Archived         *bool
}

func (s *store) updateGroup(ctx context.Context, tx pgx.Tx, id uuid.UUID, u groupUpdate) error {
	tag, err := tx.Exec(ctx, `
		UPDATE employee_groups SET
			name               = COALESCE($2, name),
			department         = COALESCE($3, department),
			trainer_profile_id = COALESCE($4, trainer_profile_id),
			archived_at        = CASE
				WHEN $5::boolean IS NULL THEN archived_at
				WHEN $5 THEN COALESCE(archived_at, now())
				ELSE NULL END
		WHERE id = $1
	`, id, u.Name, u.Department, u.TrainerProfileID, u.Archived)
	if err != nil {
		return groupWriteError(err, "правка группы")
	}
	if tag.RowsAffected() == 0 {
		return errGroupNotFound
	}
	return nil
}

func groupWriteError(err error, what string) error {
	if v, ok := pg.AsViolation(err); ok {
		switch v.Kind {
		case pg.Unique:
			return errGroupNameTaken
		case pg.ForeignKey:
			return errProfileNotFound
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (s *store) groupNames(ctx context.Context, q querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return idMap(ctx, q, `SELECT id, name FROM employee_groups WHERE id = ANY($1)`, ids, "названия групп")
}

// --- участники и сотрудники ---

func (s *store) insertSubject(ctx context.Context, tx pgx.Tx, number string, wrappedKey []byte) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO subjects (number, data_key_wrapped) VALUES ($1, $2) RETURNING id`, number, wrappedKey).Scan(&id)
	if err != nil {
		if v, ok := pg.AsViolation(err); ok && v.Kind == pg.Unique {
			return uuid.Nil, errSubjectNumberTaken
		}
		return uuid.Nil, fmt.Errorf("заведение номера участника: %w", err)
	}
	return id, nil
}

func (s *store) insertPerson(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID, p NewPerson, createdBy *uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO people (subject_id, group_id, full_name, pseudonym, personnel_no, job_title, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, subjectID, p.GroupID, p.FullName, p.Pseudonym, p.PersonnelNo, p.JobTitle, createdBy)
	if err != nil {
		return personWriteError(err, "заведение сотрудника")
	}
	return nil
}

type personUpdate struct {
	GroupID     *uuid.UUID
	FullName    *string
	Pseudonym   *string
	PersonnelNo *string
	JobTitle    *string
}

func (s *store) updatePerson(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID, u personUpdate) error {
	tag, err := tx.Exec(ctx, `
		UPDATE people SET
			group_id     = COALESCE($2, group_id),
			full_name    = COALESCE($3, full_name),
			pseudonym    = COALESCE($4, pseudonym),
			personnel_no = COALESCE($5, personnel_no),
			job_title    = COALESCE($6, job_title),
			updated_at   = now()
		WHERE subject_id = $1
	`, subjectID, u.GroupID, u.FullName, u.Pseudonym, u.PersonnelNo, u.JobTitle)
	if err != nil {
		return personWriteError(err, "правка сотрудника")
	}
	if tag.RowsAffected() == 0 {
		return errPersonNotFound
	}
	return nil
}

func personWriteError(err error, what string) error {
	if v, ok := pg.AsViolation(err); ok {
		switch {
		case v.Kind == pg.Unique && v.Constraint == "people_personnel_no_key":
			return errPersonnelNoTaken
		case v.Kind == pg.ForeignKey:
			return errGroupNotFound
		case v.Kind == pg.Check && v.Constraint == "people_has_name":
			return errPersonNeedsNameForm
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

const personColumns = `p.subject_id, s.number, p.group_id, g.name, p.full_name, p.pseudonym,
	p.personnel_no, p.job_title, p.external_ai_withdrawn_at`

const personFrom = ` FROM people p
	JOIN subjects s ON s.id = p.subject_id
	JOIN employee_groups g ON g.id = p.group_id`

func scanPerson(row pgx.Row, extra ...any) (personRow, error) {
	var r personRow
	dest := append([]any{&r.SubjectID, &r.Number, &r.GroupID, &r.GroupName, &r.FullName, &r.Pseudonym,
		&r.PersonnelNo, &r.JobTitle, &r.ExternalAIWithdrawnAt}, extra...)
	err := row.Scan(dest...)
	return r, err
}

func (s *store) person(ctx context.Context, q querier, subjectID uuid.UUID, forUpdate bool) (personRow, error) {
	sql := `SELECT ` + personColumns + personFrom + ` WHERE p.subject_id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF p`
	}
	r, err := scanPerson(q.QueryRow(ctx, sql, subjectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return personRow{}, errPersonNotFound
	}
	if err != nil {
		return personRow{}, fmt.Errorf("чтение сотрудника: %w", err)
	}
	return r, nil
}

type personFilter struct {
	// GroupIDs nil — без ограничения по группам (администратор); пустой
	// срез — ни одной группы.
	GroupIDs []uuid.UUID
	Query    *string
	Limit    int
	Offset   int
}

func (s *store) listPeople(ctx context.Context, q querier, f personFilter) ([]personRow, int, error) {
	var pattern *string
	if f.Query != nil && strings.TrimSpace(*f.Query) != "" {
		escaped := "%" + likeEscaper.Replace(strings.TrimSpace(*f.Query)) + "%"
		pattern = &escaped
	}
	rows, err := q.Query(ctx, `SELECT `+personColumns+`, count(*) OVER ()`+personFrom+`
		WHERE ($1::uuid[] IS NULL OR p.group_id = ANY($1))
		  AND ($2::text IS NULL OR p.full_name ILIKE $2 OR p.pseudonym ILIKE $2
		       OR p.personnel_no ILIKE $2 OR s.number ILIKE $2)
		ORDER BY COALESCE(p.full_name, p.pseudonym), s.number
		LIMIT $3 OFFSET $4`, f.GroupIDs, pattern, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("список сотрудников: %w", err)
	}
	defer rows.Close()
	var out []personRow
	total := 0
	for rows.Next() {
		r, err := scanPerson(rows, &total)
		if err != nil {
			return nil, 0, fmt.Errorf("список сотрудников: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("список сотрудников: %w", err)
	}
	return out, total, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (s *store) deletePerson(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM people WHERE subject_id = $1`, subjectID); err != nil {
		return fmt.Errorf("удаление строки «номер → человек»: %w", err)
	}
	return nil
}

func (s *store) destroyKey(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE subjects SET data_key_wrapped = NULL, key_destroyed_at = now()
		WHERE id = $1 AND data_key_wrapped IS NOT NULL
	`, subjectID)
	if err != nil {
		return fmt.Errorf("уничтожение ключа данных: %w", err)
	}
	return nil
}

func (s *store) markExternalAIWithdrawn(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE people SET external_ai_withdrawn_at = now(), updated_at = now() WHERE subject_id = $1`, subjectID); err != nil {
		return fmt.Errorf("отметка отзыва согласия на внешнюю нейросеть: %w", err)
	}
	return nil
}

func (s *store) wrappedKey(ctx context.Context, q querier, subjectID uuid.UUID) ([]byte, error) {
	var key []byte
	err := q.QueryRow(ctx, `SELECT data_key_wrapped FROM subjects WHERE id = $1`, subjectID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("чтение ключа данных: %w", err)
	}
	return key, nil
}

func (s *store) subjectNumbers(ctx context.Context, q querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return idMap(ctx, q, `SELECT id, number FROM subjects WHERE id = ANY($1)`, ids, "номера участников")
}

func (s *store) subjectIDByNumber(ctx context.Context, q querier, number string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM subjects WHERE number = $1`, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("поиск участника по номеру: %w", err)
	}
	return id, true, nil
}

func idMap(ctx context.Context, q querier, sql string, ids []uuid.UUID, what string) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, sql, ids)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}

func (s *store) personRefs(ctx context.Context, q querier, ids []uuid.UUID) (map[uuid.UUID]PersonRef, error) {
	out := make(map[uuid.UUID]PersonRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT s.id, s.number, p.subject_id IS NOT NULL, p.group_id, p.full_name, p.pseudonym
		FROM subjects s
		LEFT JOIN people p ON p.subject_id = s.id
		WHERE s.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("чтение участников: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r PersonRef
		var groupID *uuid.UUID
		var fullName, pseudonym *string
		if err := rows.Scan(&r.SubjectID, &r.Number, &r.Present, &groupID, &fullName, &pseudonym); err != nil {
			return nil, fmt.Errorf("чтение участников: %w", err)
		}
		if groupID != nil {
			r.GroupID = *groupID
		}
		switch {
		case fullName != nil:
			r.DisplayName, r.HasFullName = fullName, true
		case pseudonym != nil:
			r.DisplayName, r.IsPseudonym = pseudonym, true
		}
		out[r.SubjectID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение участников: %w", err)
	}
	return out, nil
}

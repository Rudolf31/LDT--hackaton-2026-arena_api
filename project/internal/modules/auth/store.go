package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/pg"
)

var (
	errUserNotFound  = errors.New("пользователь портала не найден")
	errLoginTaken    = errors.New("логин уже занят")
	errLoginFormat   = errors.New("логин не подходит по формату")
	errGroupNotFound = errors.New("группа не найдена")
)

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type userRow struct {
	ID           uuid.UUID
	Login        string
	PasswordHash string
	FullName     string
	Role         gen.Role
	IsActive     bool
	LastLoginAt  *time.Time
	GroupIDs     []uuid.UUID
}

type store struct{}

func newStore() *store { return &store{} }

const userColumns = `
	u.id, u.login, u.password_hash, u.full_name, u.role, u.is_active, u.last_login_at,
	COALESCE((SELECT array_agg(a.group_id ORDER BY a.group_id) FROM user_group_access a WHERE a.user_id = u.id), '{}')`

func scanUser(row pgx.Row) (userRow, error) {
	var r userRow
	var role string
	err := row.Scan(&r.ID, &r.Login, &r.PasswordHash, &r.FullName, &role, &r.IsActive, &r.LastLoginAt, &r.GroupIDs)
	r.Role = gen.Role(role)
	return r, err
}

func (s *store) userByLogin(ctx context.Context, q querier, login string) (userRow, error) {
	r, err := scanUser(q.QueryRow(ctx, `SELECT `+userColumns+` FROM portal_users u WHERE u.login = $1`, login))
	if errors.Is(err, pgx.ErrNoRows) {
		return userRow{}, errUserNotFound
	}
	if err != nil {
		return userRow{}, fmt.Errorf("чтение пользователя по логину: %w", err)
	}
	return r, nil
}

func (s *store) userByID(ctx context.Context, q querier, id uuid.UUID) (userRow, error) {
	r, err := scanUser(q.QueryRow(ctx, `SELECT `+userColumns+` FROM portal_users u WHERE u.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return userRow{}, errUserNotFound
	}
	if err != nil {
		return userRow{}, fmt.Errorf("чтение пользователя: %w", err)
	}
	return r, nil
}

func (s *store) listUsers(ctx context.Context, q querier) ([]userRow, error) {
	rows, err := q.Query(ctx, `SELECT `+userColumns+` FROM portal_users u ORDER BY u.full_name, u.login`)
	if err != nil {
		return nil, fmt.Errorf("список пользователей: %w", err)
	}
	defer rows.Close()
	var out []userRow
	for rows.Next() {
		r, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("список пользователей: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("список пользователей: %w", err)
	}
	return out, nil
}

func (s *store) hasUsers(ctx context.Context, q querier) (bool, error) {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM portal_users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("проверка наличия пользователей: %w", err)
	}
	return exists, nil
}

func (s *store) insertUser(ctx context.Context, tx pgx.Tx, u NewUser, passwordHash string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO portal_users (login, password_hash, full_name, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, u.Login, passwordHash, u.FullName, string(u.Role)).Scan(&id)
	if err != nil {
		return uuid.Nil, userWriteError(err, "заведение пользователя")
	}
	return id, nil
}

type userUpdate struct {
	FullName     *string
	Role         *gen.Role
	IsActive     *bool
	PasswordHash *string
}

func (s *store) updateUser(ctx context.Context, tx pgx.Tx, id uuid.UUID, u userUpdate) error {
	var role *string
	if u.Role != nil {
		r := string(*u.Role)
		role = &r
	}
	tag, err := tx.Exec(ctx, `
		UPDATE portal_users SET
			full_name     = COALESCE($2, full_name),
			role          = COALESCE($3::user_role, role),
			is_active     = COALESCE($4, is_active),
			password_hash = COALESCE($5, password_hash)
		WHERE id = $1
	`, id, u.FullName, role, u.IsActive, u.PasswordHash)
	if err != nil {
		return userWriteError(err, "правка пользователя")
	}
	if tag.RowsAffected() == 0 {
		return errUserNotFound
	}
	return nil
}

func userWriteError(err error, what string) error {
	if v, ok := pg.AsViolation(err); ok {
		switch {
		case v.Kind == pg.Unique:
			return errLoginTaken
		case v.Kind == pg.Check && v.Constraint == "portal_users_login_format":
			return errLoginFormat
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (s *store) touchLogin(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE portal_users SET last_login_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("отметка времени входа: %w", err)
	}
	return nil
}

func (s *store) accessibleGroups(ctx context.Context, q querier, userID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT COALESCE(array_agg(group_id ORDER BY group_id), '{}') FROM user_group_access WHERE user_id = $1
	`, userID).Scan(&ids)
	if err != nil {
		return nil, fmt.Errorf("группы с доступом: %w", err)
	}
	return ids, nil
}

func (s *store) hasAccess(ctx context.Context, q querier, userID, groupID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_group_access WHERE user_id = $1 AND group_id = $2)
	`, userID, groupID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("проверка доступа к группе: %w", err)
	}
	return ok, nil
}

func (s *store) insertAccess(ctx context.Context, tx pgx.Tx, userID, groupID, grantedBy uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO user_group_access (user_id, group_id, granted_by) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, userID, groupID, grantedBy)
	if err != nil {
		if v, ok := pg.AsViolation(err); ok && v.Kind == pg.ForeignKey {
			if v.Constraint == "user_group_access_user_id_fkey" {
				return errUserNotFound
			}
			return errGroupNotFound
		}
		return fmt.Errorf("выдача доступа к группе: %w", err)
	}
	return nil
}

func (s *store) deleteAccess(ctx context.Context, tx pgx.Tx, userID, groupID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_group_access WHERE user_id = $1 AND group_id = $2`, userID, groupID); err != nil {
		return fmt.Errorf("снятие доступа к группе: %w", err)
	}
	return nil
}

func (s *store) userNames(ctx context.Context, q querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, full_name FROM portal_users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("имена пользователей: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("имена пользователей: %w", err)
		}
		out[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("имена пользователей: %w", err)
	}
	return out, nil
}

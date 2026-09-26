package profiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/platform/pg"
)

var (
	errNameTaken = errors.New("профиль с таким названием уже есть")
	errNoDefault = errors.New("профиля по умолчанию нет")
)

// querier — общее у *pgxpool.Pool и pgx.Tx: чтение не решает, идёт ли
// оно в транзакции, это решает service.go.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// adminRow — профиль для администратора: с последними символами ключей.
// Сами зашифрованные ключи сюда не читаются — их читает только keys().
type adminRow struct {
	ID                    uuid.UUID
	Name                  string
	IsDefault             bool
	Settings              []byte
	Revision              int
	HasOpenRouterKey      bool
	HasModelServerToken   bool
	OpenRouterKeyLast4    *string
	ModelServerTokenLast4 *string
	KeysChangedAt         *time.Time
	KeysChangedByName     *string
	UpdatedAt             time.Time
	ArchivedAt            *time.Time
}

// publicRow — профиль для не-администратора: строится только из
// представления trainer_profiles_public, где нет ни ключей, ни их
// последних символов (FR-PF-04).
type publicRow struct {
	ID                  uuid.UUID
	Name                string
	IsDefault           bool
	Settings            []byte
	Revision            int
	HasOpenRouterKey    bool
	HasModelServerToken bool
	UpdatedAt           time.Time
	ArchivedAt          *time.Time
}

// encryptedKeys — ключи в том виде, в каком лежат в базе.
type encryptedKeys struct {
	OpenRouter       []byte
	ModelServerToken []byte
}

// keyChange — замена одного ключа: Set = поле пришло в запросе; Enc и
// Last4 nil при Set — удалить ключ.
type keyChange struct {
	Set   bool
	Enc   []byte
	Last4 *string
}

type store struct{}

func newStore() *store { return &store{} }

const adminColumns = `p.id, p.name, p.is_default, p.settings, p.revision,
	p.openrouter_key_enc IS NOT NULL, p.model_server_token_enc IS NOT NULL,
	p.openrouter_key_last4, p.model_server_token_last4,
	p.keys_changed_at, u.full_name, p.updated_at, p.archived_at`

const adminFrom = ` FROM trainer_profiles p LEFT JOIN portal_users u ON u.id = p.keys_changed_by`

func scanAdmin(row pgx.Row) (adminRow, error) {
	var r adminRow
	err := row.Scan(&r.ID, &r.Name, &r.IsDefault, &r.Settings, &r.Revision,
		&r.HasOpenRouterKey, &r.HasModelServerToken,
		&r.OpenRouterKeyLast4, &r.ModelServerTokenLast4,
		&r.KeysChangedAt, &r.KeysChangedByName, &r.UpdatedAt, &r.ArchivedAt)
	return r, err
}

const publicColumns = `id, name, is_default, settings, revision,
	has_openrouter_key, has_model_server_token, updated_at, archived_at`

func scanPublic(row pgx.Row) (publicRow, error) {
	var r publicRow
	err := row.Scan(&r.ID, &r.Name, &r.IsDefault, &r.Settings, &r.Revision,
		&r.HasOpenRouterKey, &r.HasModelServerToken, &r.UpdatedAt, &r.ArchivedAt)
	return r, err
}

// listAdmin — профиль по умолчанию первым, затем по названию. keyLast4 —
// поиск профилей с тем же ключом (UC-A-03).
func (s *store) listAdmin(ctx context.Context, q querier, includeArchived bool, keyLast4 *string) ([]adminRow, error) {
	rows, err := q.Query(ctx, `SELECT `+adminColumns+adminFrom+`
		WHERE ($1 OR p.archived_at IS NULL)
		  AND ($2::text IS NULL OR p.openrouter_key_last4 = $2 OR p.model_server_token_last4 = $2)
		ORDER BY p.is_default DESC, p.name`, includeArchived, keyLast4)
	if err != nil {
		return nil, fmt.Errorf("чтение профилей тренажёра: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (adminRow, error) { return scanAdmin(row) })
	if err != nil {
		return nil, fmt.Errorf("чтение профилей тренажёра: %w", err)
	}
	return out, nil
}

func (s *store) listPublic(ctx context.Context, q querier, includeArchived bool) ([]publicRow, error) {
	rows, err := q.Query(ctx, `SELECT `+publicColumns+` FROM trainer_profiles_public
		WHERE ($1 OR archived_at IS NULL)
		ORDER BY is_default DESC, name`, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("чтение профилей тренажёра: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (publicRow, error) { return scanPublic(row) })
	if err != nil {
		return nil, fmt.Errorf("чтение профилей тренажёра: %w", err)
	}
	return out, nil
}

func (s *store) getAdmin(ctx context.Context, q querier, id uuid.UUID, forUpdate bool) (adminRow, error) {
	sql := `SELECT ` + adminColumns + adminFrom + ` WHERE p.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF p`
	}
	r, err := scanAdmin(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return adminRow{}, ErrNotFound
	}
	if err != nil {
		return adminRow{}, fmt.Errorf("чтение профиля тренажёра: %w", err)
	}
	return r, nil
}

func (s *store) getPublic(ctx context.Context, q querier, id uuid.UUID) (publicRow, error) {
	r, err := scanPublic(q.QueryRow(ctx, `SELECT `+publicColumns+` FROM trainer_profiles_public WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return publicRow{}, ErrNotFound
	}
	if err != nil {
		return publicRow{}, fmt.Errorf("чтение профиля тренажёра: %w", err)
	}
	return r, nil
}

// defaultProfile — номер и настройки профиля по умолчанию; из них наследуются
// пустые поля остальных профилей. Читаются из представления — ключи тут
// не нужны.
func (s *store) defaultProfile(ctx context.Context, q querier) (uuid.UUID, []byte, error) {
	var id uuid.UUID
	var settings []byte
	err := q.QueryRow(ctx, `SELECT id, settings FROM trainer_profiles_public WHERE is_default`).Scan(&id, &settings)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, errNoDefault
	}
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("чтение профиля по умолчанию: %w", err)
	}
	return id, settings, nil
}

// keys — зашифрованные ключи профиля. Единственный запрос модуля, который
// читает столбцы *_enc; расшифровывает их service.
func (s *store) keys(ctx context.Context, q querier, id uuid.UUID) (encryptedKeys, error) {
	var k encryptedKeys
	err := q.QueryRow(ctx, `SELECT openrouter_key_enc, model_server_token_enc FROM trainer_profiles WHERE id = $1`, id).
		Scan(&k.OpenRouter, &k.ModelServerToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return encryptedKeys{}, ErrNotFound
	}
	if err != nil {
		return encryptedKeys{}, fmt.Errorf("чтение ключей профиля тренажёра: %w", err)
	}
	return k, nil
}

func (s *store) insert(ctx context.Context, tx pgx.Tx, name string, isDefault bool, settings []byte, actorID *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO trainer_profiles (name, is_default, settings, updated_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, name, isDefault, settings, actorID).Scan(&id)
	if err != nil {
		return uuid.Nil, mapWriteError(err, "создание профиля тренажёра")
	}
	return id, nil
}

// update — правка названия и/или настроек; ревизия +1 (FR-PF-03).
// nil — не менять.
func (s *store) update(ctx context.Context, tx pgx.Tx, id uuid.UUID, name *string, settings []byte, actorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE trainer_profiles SET
			name       = COALESCE($2, name),
			settings   = COALESCE($3::jsonb, settings),
			revision   = revision + 1,
			updated_at = now(),
			updated_by = $4
		WHERE id = $1`, id, name, settings, actorID)
	if err != nil {
		return mapWriteError(err, "сохранение профиля тренажёра")
	}
	return nil
}

// updateKeys — замена ключей. Ревизия не растёт: ключей нет в снимке
// настроек, который она описывает (D-44).
func (s *store) updateKeys(ctx context.Context, tx pgx.Tx, id uuid.UUID, openRouter, token keyChange, actorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE trainer_profiles SET
			openrouter_key_enc       = CASE WHEN $2::boolean THEN $3::bytea ELSE openrouter_key_enc END,
			openrouter_key_last4     = CASE WHEN $2::boolean THEN $4::text  ELSE openrouter_key_last4 END,
			model_server_token_enc   = CASE WHEN $5::boolean THEN $6::bytea ELSE model_server_token_enc END,
			model_server_token_last4 = CASE WHEN $5::boolean THEN $7::text  ELSE model_server_token_last4 END,
			keys_changed_at = now(),
			keys_changed_by = $8
		WHERE id = $1`,
		id, openRouter.Set, openRouter.Enc, openRouter.Last4, token.Set, token.Enc, token.Last4, actorID)
	if err != nil {
		return mapWriteError(err, "сохранение ключей профиля тренажёра")
	}
	return nil
}

func (s *store) archive(ctx context.Context, tx pgx.Tx, id uuid.UUID, actorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE trainer_profiles SET archived_at = now(), updated_at = now(), updated_by = $2
		WHERE id = $1`, id, actorID)
	if err != nil {
		return mapWriteError(err, "архивирование профиля тренажёра")
	}
	return nil
}

// sameKeyProfiles — другие профили с теми же последними символами ключа
// OpenRouter или токена (UC-A-03: где ещё стоит утёкший ключ).
func (s *store) sameKeyProfiles(ctx context.Context, q querier, id uuid.UUID, openRouterLast4, tokenLast4 *string) ([]uuid.UUID, error) {
	if openRouterLast4 == nil && tokenLast4 == nil {
		return []uuid.UUID{}, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id FROM trainer_profiles
		WHERE id <> $1
		  AND (openrouter_key_last4 = $2 OR model_server_token_last4 = $3)
		ORDER BY name`, id, openRouterLast4, tokenLast4)
	if err != nil {
		return nil, fmt.Errorf("поиск профилей с тем же ключом: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("поиск профилей с тем же ключом: %w", err)
	}
	return ids, nil
}

func mapWriteError(err error, what string) error {
	if v, ok := pg.AsViolation(err); ok && v.Kind == pg.Unique && v.Constraint == "trainer_profiles_name_key" {
		return errNameTaken
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (s *store) names(ctx context.Context, q querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, name FROM trainer_profiles_public WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("названия профилей тренажёра: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("названия профилей тренажёра: %w", err)
		}
		out[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("названия профилей тренажёра: %w", err)
	}
	return out, nil
}

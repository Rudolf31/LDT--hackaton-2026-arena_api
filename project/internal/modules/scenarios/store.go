package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/pg"
)

var (
	errScenarioNotFound = errors.New("сценарий не найден")
	errModeLocked       = errors.New("по сценарию уже есть версии — режим не меняется")
	errVersionNotFound  = errors.New("версия сценария не найдена")
)

// querier — общее у *pgxpool.Pool и pgx.Tx (как в people/store.go). Exec
// нужен setGeneration/failRunningGenerations (этап 05): UpdateGeneration
// пишет вне транзакции (задание — единственный писатель, пока идёт), а
// BeginGeneration/FinishGeneration — внутри своей.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type scenarioRow struct {
	ID                 uuid.UUID
	Slug               string
	Mode               string
	Origin             string
	ArchivedAt         *time.Time
	CreatedAt          time.Time
	CreatedBy          uuid.UUID
	CreatedByName      string
	DraftDocument      json.RawMessage
	DraftFingerprint   *string
	DraftCheck         json.RawMessage
	DraftUpdatedAt     *time.Time
	DraftUpdatedBy     *uuid.UUID
	DraftUpdatedByName *string
	Generation         json.RawMessage
	VersionsCount      int
}

type versionRow struct {
	ID                  uuid.UUID
	ScenarioID          uuid.UUID
	Number              int
	Fingerprint         string
	Format              string
	EngineVersion       string
	Document            json.RawMessage
	Mode                string
	Title               string
	Sphere              string
	NegotiationType     string
	AdmissionRehearsals *int
	AdmissionRequired   *int
	PublishedBy         uuid.UUID
	PublishedByName     string
	PublishedAt         time.Time
	SessionsCount       int // заполняется службой через VersionSessionCounter, не запросом (заглушка до этапов 07–08)
}

type store struct{}

func newStore() *store { return &store{} }

// scenarioColumns — u.full_name присоединяем join'ом на portal_users, как
// уже делает internal/modules/settings/store.go для updated_by_name: для
// одного столбца имени отдельный контракт наружу был бы лишним слоем.
const scenarioColumns = `s.id, s.slug, s.mode, s.origin, s.archived_at, s.created_at, s.created_by, cu.full_name,
	s.draft_document, s.draft_fingerprint, s.draft_check, s.draft_updated_at, s.draft_updated_by, du.full_name,
	s.generation,
	(SELECT count(*) FROM scenario_versions v WHERE v.scenario_id = s.id)`

const scenarioFrom = ` FROM scenarios s
	LEFT JOIN portal_users cu ON cu.id = s.created_by
	LEFT JOIN portal_users du ON du.id = s.draft_updated_by`

func scanScenario(row pgx.Row) (scenarioRow, error) {
	var r scenarioRow
	err := row.Scan(&r.ID, &r.Slug, &r.Mode, &r.Origin, &r.ArchivedAt, &r.CreatedAt, &r.CreatedBy, &r.CreatedByName,
		&r.DraftDocument, &r.DraftFingerprint, &r.DraftCheck, &r.DraftUpdatedAt, &r.DraftUpdatedBy, &r.DraftUpdatedByName,
		&r.Generation, &r.VersionsCount)
	return r, err
}

func (s *store) get(ctx context.Context, q querier, id uuid.UUID) (scenarioRow, error) {
	r, err := scanScenario(q.QueryRow(ctx, `SELECT `+scenarioColumns+scenarioFrom+` WHERE s.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return scenarioRow{}, errScenarioNotFound
	}
	if err != nil {
		return scenarioRow{}, fmt.Errorf("чтение сценария: %w", err)
	}
	return r, nil
}

// getForUpdate — то же, но с блокировкой строки sroci на время транзакции:
// сохранение черновика, публикация и смена режима сериализуются по
// сценарию (D-28 — сравнение ETag без гонки, FR-SC-08 — публикация видит
// последний черновик).
func (s *store) getForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (scenarioRow, error) {
	r, err := scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+scenarioFrom+` WHERE s.id = $1 FOR UPDATE OF s`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return scenarioRow{}, errScenarioNotFound
	}
	if err != nil {
		return scenarioRow{}, fmt.Errorf("чтение сценария: %w", err)
	}
	return r, nil
}

type newScenario struct {
	Slug             string
	Mode             string
	Origin           string
	DraftDocument    json.RawMessage // nil у origin=brief без документа
	DraftFingerprint *string
	DraftCheck       json.RawMessage
	DraftUpdatedAt   *time.Time
	Generation       json.RawMessage
	CreatedBy        uuid.UUID
}

func (s *store) insert(ctx context.Context, q querier, n newScenario) (uuid.UUID, error) {
	var id uuid.UUID
	var draftUpdatedBy *uuid.UUID
	if n.DraftUpdatedAt != nil {
		draftUpdatedBy = &n.CreatedBy
	}
	err := q.QueryRow(ctx, `
		INSERT INTO scenarios (slug, mode, origin, draft_document, draft_fingerprint, draft_check,
			draft_updated_at, draft_updated_by, generation, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id
	`, n.Slug, n.Mode, n.Origin, n.DraftDocument, n.DraftFingerprint, n.DraftCheck,
		n.DraftUpdatedAt, draftUpdatedBy, n.Generation, n.CreatedBy).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("заведение сценария: %w", err)
	}
	return id, nil
}

type draftUpdate struct {
	Slug             string
	Mode             *string // nil — режим не меняется
	DraftDocument    json.RawMessage
	DraftFingerprint string
	DraftCheck       json.RawMessage
	DraftUpdatedAt   time.Time
	UpdatedBy        uuid.UUID
}

// saveDraft — mode и draft_document меняются одним UPDATE, а не двумя:
// CHECK-ограничение scenarios_draft_mode сверяет их в конце каждого
// оператора по текущим значениям строки, и отдельный UPDATE mode увидел бы
// ещё не обновлённый draft_document с прежним passport.mode и упал бы на
// проверке раньше, чем FK успел бы отклонить смену режима после публикации.
// FK-ограничение scenario_versions_mode_locks_scenario (ON UPDATE RESTRICT)
// отклоняет смену, пока у сценария есть хоть одна версия (FR-MD-01) — errModeLocked.
func (s *store) saveDraft(ctx context.Context, tx pgx.Tx, id uuid.UUID, u draftUpdate) error {
	tag, err := tx.Exec(ctx, `
		UPDATE scenarios SET
			slug              = $2,
			mode              = COALESCE($3, mode),
			draft_document    = $4,
			draft_fingerprint = $5,
			draft_check       = $6,
			draft_updated_at  = $7,
			draft_updated_by  = $8
		WHERE id = $1
	`, id, u.Slug, u.Mode, u.DraftDocument, u.DraftFingerprint, u.DraftCheck, u.DraftUpdatedAt, u.UpdatedBy)
	if err != nil {
		// Проверяем имя ограничения, а не только вид нарушения: тот же UPDATE
		// пишет ещё и draft_updated_by (FK на portal_users) — его нарушение
		// не имеет отношения к смене режима и не должно превращаться в
		// mode_locked (обнаружено в код-ревью 04).
		if v, ok := pg.AsViolation(err); ok && v.Kind == pg.ForeignKey && v.Constraint == "scenario_versions_mode_locks_scenario" {
			return errModeLocked
		}
		return fmt.Errorf("сохранение черновика: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errScenarioNotFound
	}
	return nil
}

func (s *store) setArchived(ctx context.Context, tx pgx.Tx, id uuid.UUID, archived bool) error {
	tag, err := tx.Exec(ctx, `
		UPDATE scenarios SET archived_at = CASE WHEN $2 THEN COALESCE(archived_at, now()) ELSE NULL END
		WHERE id = $1
	`, id, archived)
	if err != nil {
		return fmt.Errorf("архивация сценария: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errScenarioNotFound
	}
	return nil
}

// listFilter — фильтры библиотеки сценариев (GET /scenarios). Status —
// "draft"/"published"/"archived" или пусто (черновики и опубликованные,
// без архива — архив показывается только при явном status=archived).
// OnlyPublished — ограничение наблюдателя: видит только сценарии хотя бы с
// одной версией (arena-portal-hr.md 10.5).
type listFilter struct {
	Query           *string
	Sphere          *string
	NegotiationType *string
	Mode            *string
	Status          *string
	Tag             *string
	OnlyPublished   bool
	Limit, Offset   int
}

func (s *store) list(ctx context.Context, q querier, f listFilter) ([]scenarioRow, int, error) {
	var pattern *string
	if f.Query != nil && likeQuery(*f.Query) != "" {
		p := likeQuery(*f.Query)
		pattern = &p
	}
	rows, err := q.Query(ctx, `
		SELECT s.id, s.slug, s.mode, s.origin, s.archived_at, s.created_at,
			s.draft_document, s.draft_fingerprint, s.draft_check, s.generation,
			(SELECT count(*) FROM scenario_versions v WHERE v.scenario_id = s.id),
			count(*) OVER ()
		FROM scenarios s
		WHERE
			CASE
				WHEN $1::text = 'archived'  THEN s.archived_at IS NOT NULL
				WHEN $1::text = 'published' THEN s.archived_at IS NULL AND EXISTS (SELECT 1 FROM scenario_versions v WHERE v.scenario_id = s.id)
				WHEN $1::text = 'draft'     THEN s.archived_at IS NULL AND NOT EXISTS (SELECT 1 FROM scenario_versions v WHERE v.scenario_id = s.id)
				ELSE s.archived_at IS NULL
			END
			AND (NOT $2::boolean OR EXISTS (SELECT 1 FROM scenario_versions v WHERE v.scenario_id = s.id))
			AND ($3::scenario_mode IS NULL OR s.mode = $3)
			AND ($4::text IS NULL OR s.draft_document #>> '{passport,sphere}' = $4)
			AND ($5::text IS NULL OR s.draft_document #>> '{passport,negotiation_type}' = $5)
			AND ($6::text IS NULL OR s.draft_document #> '{passport,tags}' ? $6)
			AND ($7::text IS NULL OR
				s.draft_document #>> '{passport,title}' ILIKE $7
				OR s.generation ->> 'title' ILIKE $7
				OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(s.draft_document #> '{passport,tags}', '[]'::jsonb)) t WHERE t ILIKE $7))
		ORDER BY s.created_at DESC
		LIMIT $8 OFFSET $9
	`, f.Status, f.OnlyPublished, f.Mode, f.Sphere, f.NegotiationType, f.Tag, pattern, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("список сценариев: %w", err)
	}
	defer rows.Close()

	var out []scenarioRow
	total := 0
	for rows.Next() {
		var r scenarioRow
		if err := rows.Scan(&r.ID, &r.Slug, &r.Mode, &r.Origin, &r.ArchivedAt, &r.CreatedAt,
			&r.DraftDocument, &r.DraftFingerprint, &r.DraftCheck, &r.Generation, &r.VersionsCount, &total); err != nil {
			return nil, 0, fmt.Errorf("список сценариев: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("список сценариев: %w", err)
	}
	return out, total, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likeQuery(q string) string {
	trimmed := strings.TrimSpace(q)
	if trimmed == "" {
		return ""
	}
	return "%" + likeEscaper.Replace(trimmed) + "%"
}

// --- авторство (этап 05, Authoring) ---

// setGeneration перезаписывает scenarios.generation целиком — формат
// содержимого решает generation, store в него не заглядывает.
func (s *store) setGeneration(ctx context.Context, q querier, id uuid.UUID, state json.RawMessage) error {
	tag, err := q.Exec(ctx, `UPDATE scenarios SET generation = $2 WHERE id = $1`, id, state)
	if err != nil {
		return fmt.Errorf("запись состояния авторства: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errScenarioNotFound
	}
	return nil
}

// failRunningGenerations — при старте портала (arena-api.yaml, getGeneration:
// «портал закрывает зависшие задания»): состояние конкретного задания
// (этап, попытки, расшифровка) generation не разбирает и не знает — сюда
// приходит уже готовый JSON-патч `{"status":"failed","error":{...},
// "finished_at":...}`, который через `||` дополняет то, что уже лежит в
// столбце, не трогая остальные его поля (attempts, stage, transcript…).
func (s *store) failRunningGenerations(ctx context.Context, q querier, message string) (int, error) {
	patch, err := json.Marshal(map[string]any{
		"status":      "failed",
		"error":       map[string]any{"message": message, "retry_after_seconds": nil},
		"finished_at": time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		return 0, fmt.Errorf("сериализация отказа при перезапуске: %w", err)
	}
	tag, err := q.Exec(ctx, `
		UPDATE scenarios SET generation = generation || $1::jsonb
		WHERE generation ->> 'status' = 'running'
	`, patch)
	if err != nil {
		return 0, fmt.Errorf("закрытие зависших заданий авторства: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// --- версии ---

func (s *store) maxVersionNumber(ctx context.Context, q querier, scenarioID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT COALESCE(max(number), 0) FROM scenario_versions WHERE scenario_id = $1`, scenarioID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("номер версии сценария: %w", err)
	}
	return n, nil
}

// versionNumberByFingerprint — уже опубликованная версия с этим
// отпечатком, если есть (FR-SC-09: nothing_changed при повторной
// публикации того же содержания — не только последней версии, любой).
func (s *store) versionNumberByFingerprint(ctx context.Context, q querier, scenarioID uuid.UUID, fingerprint string) (int, bool, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT number FROM scenario_versions WHERE scenario_id = $1 AND fingerprint = $2`, scenarioID, fingerprint).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("поиск версии по отпечатку: %w", err)
	}
	return n, true, nil
}

type newVersion struct {
	ScenarioID          uuid.UUID
	Number              int
	Fingerprint         string
	Format              string
	EngineVersion       string
	Document            json.RawMessage
	Mode                string
	Title               string
	Sphere              string
	NegotiationType     string
	AdmissionRehearsals int
	AdmissionRequired   int
	PublishedBy         uuid.UUID
}

func (s *store) insertVersion(ctx context.Context, tx pgx.Tx, n newVersion) (versionRow, error) {
	var r versionRow
	r.ScenarioID = n.ScenarioID
	err := tx.QueryRow(ctx, `
		INSERT INTO scenario_versions (scenario_id, number, fingerprint, format, engine_version, document, mode,
			title, sphere, negotiation_type, admission_rehearsals, admission_required, published_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, number, fingerprint, format, engine_version, document, mode, title, sphere, negotiation_type,
			admission_rehearsals, admission_required, published_by, published_at
	`, n.ScenarioID, n.Number, n.Fingerprint, n.Format, n.EngineVersion, n.Document, n.Mode,
		n.Title, n.Sphere, n.NegotiationType, n.AdmissionRehearsals, n.AdmissionRequired, n.PublishedBy).
		Scan(&r.ID, &r.Number, &r.Fingerprint, &r.Format, &r.EngineVersion, &r.Document, &r.Mode, &r.Title, &r.Sphere,
			&r.NegotiationType, &r.AdmissionRehearsals, &r.AdmissionRequired, &r.PublishedBy, &r.PublishedAt)
	if err != nil {
		return versionRow{}, fmt.Errorf("публикация версии сценария: %w", err)
	}
	return r, nil
}

const versionColumns = `v.id, v.scenario_id, v.number, v.fingerprint, v.format, v.engine_version, v.document, v.mode,
	v.title, v.sphere, v.negotiation_type, v.admission_rehearsals, v.admission_required,
	v.published_by, pu.full_name, v.published_at`

const versionFrom = ` FROM scenario_versions v LEFT JOIN portal_users pu ON pu.id = v.published_by`

func scanVersion(row pgx.Row) (versionRow, error) {
	var r versionRow
	err := row.Scan(&r.ID, &r.ScenarioID, &r.Number, &r.Fingerprint, &r.Format, &r.EngineVersion, &r.Document, &r.Mode,
		&r.Title, &r.Sphere, &r.NegotiationType, &r.AdmissionRehearsals, &r.AdmissionRequired,
		&r.PublishedBy, &r.PublishedByName, &r.PublishedAt)
	return r, err
}

func (s *store) listVersions(ctx context.Context, q querier, scenarioID uuid.UUID) ([]versionRow, error) {
	rows, err := q.Query(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.scenario_id = $1 ORDER BY v.number`, scenarioID)
	if err != nil {
		return nil, fmt.Errorf("список версий сценария: %w", err)
	}
	defer rows.Close()
	var out []versionRow
	for rows.Next() {
		r, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("список версий сценария: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("список версий сценария: %w", err)
	}
	return out, nil
}

// latestVersion — версия с наибольшим номером, если версии есть.
func (s *store) latestVersion(ctx context.Context, q querier, scenarioID uuid.UUID) (versionRow, bool, error) {
	r, err := scanVersion(q.QueryRow(ctx, `SELECT `+versionColumns+versionFrom+`
		WHERE v.scenario_id = $1 ORDER BY v.number DESC LIMIT 1`, scenarioID))
	if errors.Is(err, pgx.ErrNoRows) {
		return versionRow{}, false, nil
	}
	if err != nil {
		return versionRow{}, false, fmt.Errorf("чтение последней версии сценария: %w", err)
	}
	return r, true, nil
}

func (s *store) versionByNumber(ctx context.Context, q querier, scenarioID uuid.UUID, number int) (versionRow, error) {
	r, err := scanVersion(q.QueryRow(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.scenario_id = $1 AND v.number = $2`, scenarioID, number))
	if errors.Is(err, pgx.ErrNoRows) {
		return versionRow{}, errVersionNotFound
	}
	if err != nil {
		return versionRow{}, fmt.Errorf("чтение версии сценария: %w", err)
	}
	return r, nil
}

func (s *store) versionByID(ctx context.Context, q querier, id uuid.UUID) (versionRow, error) {
	r, err := scanVersion(q.QueryRow(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return versionRow{}, errVersionNotFound
	}
	if err != nil {
		return versionRow{}, fmt.Errorf("чтение версии сценария: %w", err)
	}
	return r, nil
}

func (s *store) scenarioArchived(ctx context.Context, q querier, id uuid.UUID) (bool, error) {
	var archived bool
	if err := q.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM scenarios WHERE id = $1`, id).Scan(&archived); err != nil {
		return false, fmt.Errorf("чтение сценария версии: %w", err)
	}
	return archived, nil
}

func (s *store) versionBriefs(ctx context.Context, q querier, ids []uuid.UUID) (map[uuid.UUID]VersionBrief, error) {
	out := make(map[uuid.UUID]VersionBrief, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT v.id, v.scenario_id, v.number, v.mode, v.title,
		       EXISTS (SELECT 1 FROM scenario_versions n WHERE n.scenario_id = v.scenario_id AND n.number > v.number)
		FROM scenario_versions v
		WHERE v.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("чтение версий сценариев: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b VersionBrief
		var mode string
		if err := rows.Scan(&b.ID, &b.ScenarioID, &b.Number, &mode, &b.Title, &b.NewerExists); err != nil {
			return nil, fmt.Errorf("чтение версий сценариев: %w", err)
		}
		b.Mode = gen.Mode(mode)
		out[b.ID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение версий сценариев: %w", err)
	}
	return out, nil
}

func (s *store) versionIDs(ctx context.Context, q querier, scenarioID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT id FROM scenario_versions WHERE scenario_id = $1`, scenarioID)
	if err != nil {
		return nil, fmt.Errorf("версии сценария: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("версии сценария: %w", err)
	}
	return ids, nil
}

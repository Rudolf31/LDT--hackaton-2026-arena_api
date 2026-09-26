package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/scenariodoc"
	"arena-portal-backend/internal/modules/settings"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

type service struct {
	pool       *pgxpool.Pool
	store      *store
	audit      audit.Writer
	settings   settings.Service
	rehearsals RehearsalCounter
	sessions   VersionSessionCounter
}

func newService(pool *pgxpool.Pool, auditWriter audit.Writer, settingsSvc settings.Service,
	rehearsals RehearsalCounter, sessionCounts VersionSessionCounter) *service {
	return &service{pool: pool, store: newStore(), audit: auditWriter, settings: settingsSvc,
		rehearsals: rehearsals, sessions: sessionCounts}
}

var _ Versions = (*service)(nil)

// --- ошибки-ответы ---

func errScenarioMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Такого сценария нет.")
}
func errVersionMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Такой версии у сценария нет.")
}
func errArchived() *httpx.Error {
	return httpx.NewError(httpx.KindArchived, "Сценарий в архиве — сначала верните его из архива.")
}
func errDraftChanged() *httpx.Error {
	return httpx.NewError(httpx.KindDraftChanged, "Черновик успел измениться — обновите экран и попробуйте снова.")
}
func errModeLockedConflict() *httpx.Error {
	return httpx.NewError(httpx.KindModeLocked, "По сценарию уже есть версии — режим не меняется; сделайте копию.")
}
func errGenerationRunning() *httpx.Error {
	return httpx.NewError(httpx.KindGenerationRunning, "Генерация уже идёт.")
}
func errGenerationBlocksDraft() *httpx.Error {
	return httpx.NewError(httpx.KindGenerationRunning, "Идёт генерация — дождитесь её окончания.")
}
func errDraftMissingForEdit() *httpx.Error {
	return httpx.NewError(httpx.KindDraftMissing, "Сначала создайте черновик.")
}

func fieldError(path, message string) *httpx.Error {
	return httpx.NewError(httpx.KindValidationFailed, message).WithErrors([]gen.FieldError{{Path: path, Message: message}})
}

// --- создание ---

type createFields struct {
	Origin           gen.Origin
	Mode             gen.Mode
	TemplateID       *string
	Title            *string
	SourceScenarioID *uuid.UUID
	SourceVersion    *int
}

type scenarioDetail struct {
	Row           scenarioRow
	LatestVersion *versionRow
	Admission     admissionView
}

func (s *service) Create(ctx context.Context, a actor.Actor, f createFields) (scenarioDetail, error) {
	if f.Origin == gen.OriginBrief {
		return s.createBrief(ctx, a, f)
	}
	document, err := s.buildDocument(ctx, f)
	if err != nil {
		return scenarioDetail{}, err
	}
	return s.createWithDocument(ctx, a, f.Origin, document)
}

// createBrief — сценарий под авторство (D-26): без документа, только
// название в scenarios.generation; документ появится на этапе 05.
func (s *service) createBrief(ctx context.Context, a actor.Actor, f createFields) (scenarioDetail, error) {
	title := ""
	if f.Title != nil {
		title = strings.TrimSpace(*f.Title)
	}
	if title == "" {
		return scenarioDetail{}, fieldError("/title", "Укажите название сценария.")
	}
	generation, err := json.Marshal(map[string]any{"status": "idle", "title": title})
	if err != nil {
		return scenarioDetail{}, fmt.Errorf("сериализация состояния авторства: %w", err)
	}
	id, err := s.store.insert(ctx, s.pool, newScenario{
		Slug: "scenario", Mode: string(f.Mode), Origin: string(gen.OriginBrief),
		Generation: generation, CreatedBy: a.UserID,
	})
	if err != nil {
		return scenarioDetail{}, fmt.Errorf("заведение сценария из брифа: %w", err)
	}
	return s.Get(ctx, a, id)
}

func (s *service) buildDocument(ctx context.Context, f createFields) ([]byte, error) {
	switch f.Origin {
	case gen.OriginTemplate:
		return s.buildFromTemplate(f)
	case gen.OriginManual:
		return s.buildManual(f)
	case gen.OriginCopy:
		return s.buildCopy(ctx, f)
	default:
		return nil, fieldError("/origin", "Неизвестное происхождение сценария.")
	}
}

func (s *service) buildFromTemplate(f createFields) ([]byte, error) {
	if f.TemplateID == nil || strings.TrimSpace(*f.TemplateID) == "" {
		return nil, fieldError("/template_id", "Укажите шаблон.")
	}
	var found *scenariodoc.Template
	for _, t := range scenariodoc.Templates() {
		if t.ID == *f.TemplateID {
			tt := t
			found = &tt
			break
		}
	}
	if found == nil {
		return nil, fieldError("/template_id", "Такого шаблона нет.")
	}
	tree, err := decodeTree(found.Document)
	if err != nil {
		return nil, fmt.Errorf("разбор шаблона %s: %w", found.ID, err)
	}
	setPassport(tree, f.Mode, gen.OriginTemplate, f.Title)
	setAuthoring(tree, map[string]any{"source_template": found.ID})
	return encodeTree(tree)
}

func (s *service) buildManual(f createFields) ([]byte, error) {
	tree, err := decodeTree(scenariodoc.Skeleton())
	if err != nil {
		return nil, fmt.Errorf("разбор каркаса: %w", err)
	}
	setPassport(tree, f.Mode, gen.OriginManual, f.Title)
	return encodeTree(tree)
}

func (s *service) buildCopy(ctx context.Context, f createFields) ([]byte, error) {
	if f.SourceScenarioID == nil {
		return nil, fieldError("/source_scenario_id", "Укажите исходный сценарий.")
	}
	title := ""
	if f.Title != nil {
		title = strings.TrimSpace(*f.Title)
	}
	if title == "" {
		return nil, fieldError("/title", "Укажите название копии.")
	}
	document, srcVersion, err := s.sourceDocument(ctx, *f.SourceScenarioID, f.SourceVersion)
	if err != nil {
		return nil, err
	}
	tree, err := decodeTree(document)
	if err != nil {
		return nil, fmt.Errorf("разбор документа источника: %w", err)
	}
	setPassport(tree, f.Mode, gen.OriginCopy, &title)
	setAuthoring(tree, map[string]any{
		"source_template": "",
		"copied_from":     map[string]any{"scenario_id": f.SourceScenarioID.String(), "version": srcVersion},
	})
	return encodeTree(tree)
}

// sourceDocument — документ, с которого начинается копия: заданная версия,
// иначе последняя опубликованная, иначе черновик источника (UC-M-03).
func (s *service) sourceDocument(ctx context.Context, id uuid.UUID, version *int) (json.RawMessage, int, error) {
	src, err := s.store.get(ctx, s.pool, id)
	if errors.Is(err, errScenarioNotFound) {
		return nil, 0, fieldError("/source_scenario_id", "Такого сценария нет.")
	}
	if err != nil {
		return nil, 0, err
	}
	if version != nil {
		v, err := s.store.versionByNumber(ctx, s.pool, id, *version)
		if errors.Is(err, errVersionNotFound) {
			return nil, 0, fieldError("/source_version", "Такой версии у исходного сценария нет.")
		}
		if err != nil {
			return nil, 0, err
		}
		return v.Document, v.Number, nil
	}
	if latest, ok, err := s.store.latestVersion(ctx, s.pool, id); err != nil {
		return nil, 0, err
	} else if ok {
		return latest.Document, latest.Number, nil
	}
	if src.DraftDocument == nil {
		return nil, 0, fieldError("/source_scenario_id", "У исходного сценария ещё нет содержания для копирования.")
	}
	return src.DraftDocument, 0, nil
}

// createWithDocument — общий путь для шаблона, каркаса, копии и импорта:
// схемная ошибка блокирует заведение (FR-SC-15 применена и к созданию, не
// только к сохранению черновика — иначе Import мог бы завести сценарий,
// который потом никогда не сохранится).
func (s *service) createWithDocument(ctx context.Context, a actor.Actor, origin gen.Origin, document []byte) (scenarioDetail, error) {
	diags := scenariodoc.Validate(document)
	if hasSchemaError(diags) {
		return scenarioDetail{}, httpx.NewError(httpx.KindValidationFailed, "Документ не проходит проверку схемы.").
			WithErrors(toFieldErrors(diags, isSchemaError))
	}
	checkJSON, err := json.Marshal(toCheckResult(diags))
	if err != nil {
		return scenarioDetail{}, fmt.Errorf("сериализация результата проверки: %w", err)
	}
	fingerprint, err := scenariodoc.Fingerprint(document)
	if err != nil {
		return scenarioDetail{}, fieldError("", "Документ сценария повреждён — не удалось посчитать отпечаток.")
	}
	tree, err := decodeTree(document)
	if err != nil {
		return scenarioDetail{}, fmt.Errorf("разбор документа: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	id, err := s.store.insert(ctx, s.pool, newScenario{
		Slug: passportField(tree, "id"), Mode: passportField(tree, "mode"), Origin: string(origin),
		DraftDocument: document, DraftFingerprint: &fingerprint, DraftCheck: checkJSON,
		DraftUpdatedAt: &now, CreatedBy: a.UserID,
	})
	if err != nil {
		return scenarioDetail{}, fmt.Errorf("заведение сценария: %w", err)
	}
	return s.Get(ctx, a, id)
}

// Import — тело уже разобрано httpx.Body как JSON-объект (проверка тела —
// D-14, arena-scenario.schema.json заменена на «любой объект»); собственно
// формат проверяет scenariodoc через createWithDocument.
func (s *service) Import(ctx context.Context, a actor.Actor, document []byte) (scenarioDetail, error) {
	tree, err := decodeTree(document)
	if err != nil {
		return scenarioDetail{}, fieldError("", "Файл — не корректный JSON-объект.")
	}
	origin := gen.Origin(passportField(tree, "origin"))
	switch origin {
	case gen.OriginBrief, gen.OriginTemplate, gen.OriginCopy, gen.OriginManual:
	default:
		origin = gen.OriginManual
	}
	return s.createWithDocument(ctx, a, origin, document)
}

// --- чтение сценария и библиотеки ---

func (s *service) Get(ctx context.Context, a actor.Actor, id uuid.UUID) (scenarioDetail, error) {
	row, err := s.store.get(ctx, s.pool, id)
	if errors.Is(err, errScenarioNotFound) {
		return scenarioDetail{}, errScenarioMissing()
	}
	if err != nil {
		return scenarioDetail{}, err
	}
	// Наблюдатель видит только опубликованные сценарии (arena-portal-hr.md
	// 10.5) — у сценария без версий для него как будто ничего нет.
	if a.Role == gen.Observer && row.VersionsCount == 0 {
		return scenarioDetail{}, errScenarioMissing()
	}
	d := scenarioDetail{Row: row}
	// Гвардия по row.VersionsCount (тот же снимок строки, что и остальные
	// поля row), а не безусловный запрос: без неё окно между этим SELECT и
	// latestVersion могло дать рассинхронизацию (row.VersionsCount = 0, но
	// LatestVersion уже заполнен, если версия опубликовалась конкурентно
	// между двумя запросами) — так же, как уже сделано в List ниже.
	if row.VersionsCount > 0 {
		if latest, ok, err := s.store.latestVersion(ctx, s.pool, id); err != nil {
			return scenarioDetail{}, err
		} else if ok {
			d.LatestVersion = &latest
		}
	}
	admission, err := s.admissionFor(ctx, id, row.DraftFingerprint, nil)
	if err != nil {
		return scenarioDetail{}, err
	}
	d.Admission = admission
	return d, nil
}

type listParams struct {
	Query, Sphere, NegotiationType, Mode, Status, Tag *string
	Limit, Offset                                     int
}

type listResult struct {
	Items []scenarioDetail
	Total int
}

func (s *service) List(ctx context.Context, a actor.Actor, p listParams) (listResult, error) {
	f := listFilter{
		Query: p.Query, Sphere: p.Sphere, NegotiationType: p.NegotiationType, Mode: p.Mode,
		Status: p.Status, Tag: p.Tag, Limit: p.Limit, Offset: p.Offset,
	}
	if a.Role == gen.Observer {
		f.OnlyPublished = true
	}
	rows, total, err := s.store.list(ctx, s.pool, f)
	if err != nil {
		return listResult{}, err
	}
	items := make([]scenarioDetail, 0, len(rows))
	for _, r := range rows {
		d := scenarioDetail{Row: r}
		if r.VersionsCount > 0 {
			if latest, ok, err := s.store.latestVersion(ctx, s.pool, r.ID); err != nil {
				return listResult{}, err
			} else if ok {
				d.LatestVersion = &latest
			}
		}
		// Admission сюда не считаем: toScenarioListItem (единственный
		// потребитель List) его не читает — только toScenarioCard (Get/
		// Create/Import/Archive). Раньше это давало settings.Get и
		// rehearsals.Count на каждую строку страницы библиотеки без всякой
		// пользы (найдено в код-ревью 04).
		items = append(items, d)
	}
	return listResult{Items: items, Total: total}, nil
}

func (s *service) Archive(ctx context.Context, a actor.Actor, id uuid.UUID, archived bool) (scenarioDetail, error) {
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.store.setArchived(ctx, tx, id, archived); err != nil {
			if errors.Is(err, errScenarioNotFound) {
				return errScenarioMissing()
			}
			return err
		}
		return s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionScenarioArchived, Outcome: audit.OutcomeOK,
			Details: map[string]any{"scenario_id": id.String(), "archived": archived},
		})
	})
	if err != nil {
		return scenarioDetail{}, err
	}
	return s.Get(ctx, a, id)
}

// --- черновик ---

type admissionView struct {
	Fingerprint *string
	Done        int
	Required    int
	Reset       *bool
}

type draftView struct {
	ScenarioID    uuid.UUID
	Document      json.RawMessage
	Fingerprint   string
	Check         gen.CheckResult
	Admission     admissionView
	UpdatedAt     time.Time
	UpdatedByName *string
	ETag          string
}

func (s *service) admissionFor(ctx context.Context, scenarioID uuid.UUID, fingerprint *string, reset *bool) (admissionView, error) {
	st, err := s.settings.Get(ctx)
	if err != nil {
		return admissionView{}, err
	}
	av := admissionView{Fingerprint: fingerprint, Required: st.AdmissionRehearsals, Reset: reset}
	if fingerprint != nil {
		done, err := s.rehearsals.Count(ctx, scenarioID, *fingerprint)
		if err != nil {
			return admissionView{}, err
		}
		av.Done = done
	}
	return av, nil
}

func (s *service) GetDraft(ctx context.Context, a actor.Actor, id uuid.UUID) (draftView, error) {
	row, err := s.store.get(ctx, s.pool, id)
	if errors.Is(err, errScenarioNotFound) {
		return draftView{}, errScenarioMissing()
	}
	if err != nil {
		return draftView{}, err
	}
	if row.DraftDocument == nil {
		return draftView{}, httpx.NewError(httpx.KindNotFound, "У сценария ещё нет черновика — авторство ещё не завершено.")
	}
	check, err := decodeCheckResult(row.DraftCheck)
	if err != nil {
		return draftView{}, err
	}
	admission, err := s.admissionFor(ctx, row.ID, row.DraftFingerprint, nil)
	if err != nil {
		return draftView{}, err
	}
	return draftView{
		ScenarioID: row.ID, Document: row.DraftDocument, Fingerprint: derefStr(row.DraftFingerprint),
		Check: check, Admission: admission, UpdatedAt: derefTime(row.DraftUpdatedAt),
		UpdatedByName: row.DraftUpdatedByName, ETag: draftETag(row.DraftUpdatedAt),
	}, nil
}

// SaveDraft — документ заменяется целиком (arena-api.yaml, saveDraft).
// Схемная ошибка не сохраняется (FR-SC-15); смена passport.mode до первой
// публикации сама переводит scenarios.mode и пишет scenario_mode_changed
// той же транзакцией, после публикации — 409 mode_locked (FR-MD-01).
func (s *service) SaveDraft(ctx context.Context, a actor.Actor, id uuid.UUID, document []byte, ifMatch *string) (draftView, error) {
	diags := scenariodoc.Validate(document)
	if hasSchemaError(diags) {
		return draftView{}, httpx.NewError(httpx.KindValidationFailed, "Документ не проходит проверку схемы.").
			WithErrors(toFieldErrors(diags, isSchemaError))
	}
	fingerprint, err := scenariodoc.Fingerprint(document)
	if err != nil {
		return draftView{}, fieldError("", "Документ сценария повреждён — не удалось посчитать отпечаток.")
	}
	tree, err := decodeTree(document)
	if err != nil {
		return draftView{}, fmt.Errorf("разбор документа: %w", err)
	}
	docMode := passportField(tree, "mode")
	slug := passportField(tree, "id")
	checkJSON, err := json.Marshal(toCheckResult(diags))
	if err != nil {
		return draftView{}, fmt.Errorf("сериализация результата проверки: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)

	var result draftView
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.store.getForUpdate(ctx, tx, id)
		if errors.Is(err, errScenarioNotFound) {
			return errScenarioMissing()
		}
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil {
			return errArchived()
		}
		// Идёт генерация (этап 05, «решения по умолчанию» плана): она сама
		// пишет черновик по итогу задания, и ручная правка в это время
		// затёрла бы её результат или сама была бы затёрта им — 409, а не
		// гонка результатов.
		if generationStatus(row.Generation) == "running" {
			return errGenerationBlocksDraft()
		}
		if ifMatch != nil && *ifMatch != draftETag(row.DraftUpdatedAt) {
			return errDraftChanged()
		}
		var modeChange *string
		if docMode != "" && docMode != row.Mode {
			modeChange = &docMode
		}
		if err := s.store.saveDraft(ctx, tx, id, draftUpdate{
			Slug: slug, Mode: modeChange, DraftDocument: document, DraftFingerprint: fingerprint, DraftCheck: checkJSON,
			DraftUpdatedAt: now, UpdatedBy: a.UserID,
		}); err != nil {
			if errors.Is(err, errModeLocked) {
				return errModeLockedConflict()
			}
			return err
		}
		if modeChange != nil {
			if err := s.audit.Write(ctx, tx, audit.Entry{
				ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
				Action: gen.AuditActionScenarioModeChanged, Outcome: audit.OutcomeOK,
				Details: map[string]any{"scenario_id": id.String(), "from": row.Mode, "to": docMode},
			}); err != nil {
				return err
			}
		}
		updated, err := s.store.get(ctx, tx, id)
		if err != nil {
			return err
		}
		reset := row.DraftFingerprint == nil || *row.DraftFingerprint != fingerprint
		admission, err := s.admissionFor(ctx, id, &fingerprint, &reset)
		if err != nil {
			return err
		}
		check, err := decodeCheckResult(updated.DraftCheck)
		if err != nil {
			return err
		}
		result = draftView{
			ScenarioID: id, Document: updated.DraftDocument, Fingerprint: fingerprint, Check: check,
			Admission: admission, UpdatedAt: derefTime(updated.DraftUpdatedAt),
			UpdatedByName: updated.DraftUpdatedByName, ETag: draftETag(updated.DraftUpdatedAt),
		}
		return nil
	})
	if err != nil {
		return draftView{}, err
	}
	return result, nil
}

type dryRunView struct {
	Check           gen.CheckResult
	Fingerprint     string
	ResetsAdmission bool
}

func (s *service) CheckDraft(ctx context.Context, a actor.Actor, id uuid.UUID, document []byte) (dryRunView, error) {
	row, err := s.store.get(ctx, s.pool, id)
	if errors.Is(err, errScenarioNotFound) {
		return dryRunView{}, errScenarioMissing()
	}
	if err != nil {
		return dryRunView{}, err
	}
	diags := scenariodoc.Validate(document)
	fingerprint, err := scenariodoc.Fingerprint(document)
	if err != nil {
		return dryRunView{}, fieldError("", "Документ сценария повреждён — не удалось посчитать отпечаток.")
	}
	resets := row.DraftFingerprint == nil || *row.DraftFingerprint != fingerprint
	return dryRunView{Check: toCheckResult(diags), Fingerprint: fingerprint, ResetsAdmission: resets}, nil
}

// --- публикация и версии ---

func (s *service) Publish(ctx context.Context, a actor.Actor, id uuid.UUID, fingerprint string) (versionRow, error) {
	var result versionRow
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.store.getForUpdate(ctx, tx, id)
		if errors.Is(err, errScenarioNotFound) {
			return errScenarioMissing()
		}
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil {
			return errArchived()
		}
		if row.DraftDocument == nil || row.DraftFingerprint == nil {
			return httpx.NewError(httpx.KindScenarioCheckFailed, "У сценария ещё нет черновика — сначала создайте документ.")
		}
		if *row.DraftFingerprint != fingerprint {
			return httpx.NewError(httpx.KindFingerprintChanged,
				"Черновик изменился с тех пор, как вы его проверяли — обновите экран и попробуйте снова.")
		}

		if n, found, err := s.store.versionNumberByFingerprint(ctx, tx, id, fingerprint); err != nil {
			return err
		} else if found {
			latestNumber, err := s.store.maxVersionNumber(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == latestNumber {
				return httpx.NewError(httpx.KindNothingChanged, "Содержание не изменилось — публиковать нечего.")
			}
			return httpx.NewError(httpx.KindNothingChanged, fmt.Sprintf("Такое содержание уже опубликовано как версия %d.", n))
		}

		diags := scenariodoc.Validate(row.DraftDocument)
		if !scenariodoc.Publishable(diags) {
			return httpx.NewError(httpx.KindScenarioCheckFailed, "Сценарий не проходит проверку — есть блокирующие ошибки.").
				WithErrors(toFieldErrors(diags, isBlockingError))
		}

		number, err := s.store.maxVersionNumber(ctx, tx, id)
		if err != nil {
			return err
		}
		number++

		tree, err := decodeTree(row.DraftDocument)
		if err != nil {
			return fmt.Errorf("разбор черновика: %w", err)
		}
		setPassportVersion(tree, number)
		versionDoc, err := encodeTree(tree)
		if err != nil {
			return fmt.Errorf("сериализация версии: %w", err)
		}

		rehearsalsDone, err := s.rehearsals.Count(ctx, id, fingerprint)
		if err != nil {
			return err
		}

		inserted, err := s.store.insertVersion(ctx, tx, newVersion{
			ScenarioID: id, Number: number, Fingerprint: fingerprint, Format: topField(tree, "format"),
			EngineVersion: scenariodoc.EngineVersion, Document: versionDoc, Mode: row.Mode,
			Title: passportField(tree, "title"), Sphere: passportField(tree, "sphere"),
			NegotiationType:     passportField(tree, "negotiation_type"),
			AdmissionRehearsals: rehearsalsDone, AdmissionRequired: 0, PublishedBy: a.UserID,
		})
		if err != nil {
			return err
		}

		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionScenarioPublished, Outcome: audit.OutcomeOK,
			Details: map[string]any{"scenario_id": id.String(), "version": number, "fingerprint": fingerprint, "mode": row.Mode},
		}); err != nil {
			return err
		}

		full, err := s.store.versionByID(ctx, tx, inserted.ID)
		if err != nil {
			return err
		}
		result = full
		return nil
	})
	if err != nil {
		return versionRow{}, err
	}
	return s.withSessionCount(ctx, result)
}

func (s *service) ListVersions(ctx context.Context, a actor.Actor, id uuid.UUID) ([]versionRow, error) {
	if _, err := s.store.get(ctx, s.pool, id); err != nil {
		if errors.Is(err, errScenarioNotFound) {
			return nil, errScenarioMissing()
		}
		return nil, err
	}
	rows, err := s.store.listVersions(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	return s.withSessionCounts(ctx, rows)
}

func (s *service) GetVersion(ctx context.Context, a actor.Actor, id uuid.UUID, number int) (versionRow, error) {
	if _, err := s.store.get(ctx, s.pool, id); err != nil {
		if errors.Is(err, errScenarioNotFound) {
			return versionRow{}, errScenarioMissing()
		}
		return versionRow{}, err
	}
	v, err := s.store.versionByNumber(ctx, s.pool, id, number)
	if errors.Is(err, errVersionNotFound) {
		return versionRow{}, errVersionMissing()
	}
	if err != nil {
		return versionRow{}, err
	}
	return s.withSessionCount(ctx, v)
}

func (s *service) withSessionCount(ctx context.Context, v versionRow) (versionRow, error) {
	rows, err := s.withSessionCounts(ctx, []versionRow{v})
	if err != nil {
		return versionRow{}, err
	}
	return rows[0], nil
}

func (s *service) withSessionCounts(ctx context.Context, rows []versionRow) ([]versionRow, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := s.sessions.Counts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].SessionsCount = counts[rows[i].ID]
	}
	return rows, nil
}

type exportResult struct {
	Document json.RawMessage
	Filename string
}

func (s *service) Export(ctx context.Context, a actor.Actor, id uuid.UUID, version *int) (exportResult, error) {
	row, err := s.store.get(ctx, s.pool, id)
	if errors.Is(err, errScenarioNotFound) {
		return exportResult{}, errScenarioMissing()
	}
	if err != nil {
		return exportResult{}, err
	}
	if version == nil {
		if row.DraftDocument == nil {
			return exportResult{}, httpx.NewError(httpx.KindNotFound, "У сценария ещё нет черновика для экспорта.")
		}
		return exportResult{Document: row.DraftDocument, Filename: row.Slug + "-draft.json"}, nil
	}
	v, err := s.store.versionByNumber(ctx, s.pool, id, *version)
	if errors.Is(err, errVersionNotFound) {
		return exportResult{}, errVersionMissing()
	}
	if err != nil {
		return exportResult{}, err
	}
	return exportResult{Document: v.Document, Filename: fmt.Sprintf("%s-v%d.json", row.Slug, v.Number)}, nil
}

// --- шаблоны ---

// Templates — шесть шаблонов сфер, без демо-шаблона (он не входит в
// «шесть равноправных» и служит только заготовкой для модуля demo, этап 11).
func (s *service) Templates() []scenariodoc.Template {
	all := scenariodoc.Templates()
	out := make([]scenariodoc.Template, 0, len(all))
	for _, t := range all {
		if t.ID == "demo" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// TemplateList — то же, что Templates, уже в виде ответа контракта. Живёт
// здесь, а не в transport.go: тот файл не импортирует чужой модуль
// напрямую (D-18, .golangci.yml — modules-transport-no-cross-import), а
// вычислить веса доводов шаблона может только scenariodoc.
func (s *service) TemplateList() []gen.ScenarioTemplate {
	templates := s.Templates()
	out := make([]gen.ScenarioTemplate, 0, len(templates))
	for _, tpl := range templates {
		weights := scenariodoc.MainWeights(tpl.Document)
		item := gen.ScenarioTemplate{Id: tpl.ID, Title: tpl.Title, Description: tpl.Description, Sphere: gen.Sphere(tpl.Sphere)}
		for _, w := range weights {
			evidence, label, weight := w.Evidence, w.Label, float32(w.Weight)
			item.MainWeights = append(item.MainWeights, struct {
				Evidence *string  `json:"evidence,omitempty"`
				Label    *string  `json:"label,omitempty"`
				Weight   *float32 `json:"weight,omitempty"`
			}{Evidence: &evidence, Label: &label, Weight: &weight})
		}
		out = append(out, item)
	}
	return out
}

// passportOrGeneration — название, сфера, тип переговоров и теги: из
// паспорта черновика, а у сценария, который ещё генерируется из брифа и
// черновика не имеет, — из scenarios.generation (ScenarioListItem,
// arena-api.yaml). Сфера у сценария без документа условна («другое») — её
// ещё не из чего взять.
func passportOrGeneration(row scenarioRow) (title string, sphere gen.Sphere, negotiationType *gen.NegotiationType, tags []string, generating bool) {
	tags = []string{}
	sphere = gen.Other
	if row.DraftDocument != nil {
		if p, err := scenariodoc.Passport(row.DraftDocument); err == nil {
			title = p.Title
			sphere = gen.Sphere(p.Sphere)
			if p.NegotiationType != "" {
				nt := gen.NegotiationType(p.NegotiationType)
				negotiationType = &nt
			}
			if p.Tags != nil {
				tags = p.Tags
			}
			return
		}
	}
	var g struct {
		Status string `json:"status"`
		Title  string `json:"title"`
	}
	if row.Generation != nil {
		// Ошибка не пробрасывается — это вспомогательное поле для карточки
		// библиотеки (правило 8 не про такие read-only витрины, см.
		// MainWeights в scenariodoc), но и не должна пропадать бесследно:
		// повреждённая scenarios.generation — признак дефекта в записавшем
		// её коде (этап 05), и это стоит видеть в логе (найдено в код-ревью 04).
		if err := json.Unmarshal(row.Generation, &g); err != nil {
			log.Printf("scenarios: не удалось разобрать generation сценария %s: %v", row.ID, err)
		}
	}
	title = g.Title
	generating = g.Status == "running"
	return
}

// --- Versions (контракт наружу) ---

func (s *service) Version(ctx context.Context, id uuid.UUID) (VersionInfo, error) {
	v, err := s.store.versionByID(ctx, s.pool, id)
	if errors.Is(err, errVersionNotFound) {
		return VersionInfo{}, ErrVersionNotFound
	}
	if err != nil {
		return VersionInfo{}, err
	}
	return VersionInfo{
		ID: v.ID, ScenarioID: v.ScenarioID, Number: v.Number, Mode: gen.Mode(v.Mode),
		Fingerprint: v.Fingerprint, Format: v.Format, EngineVersion: v.EngineVersion,
		Title: v.Title, Sphere: gen.Sphere(v.Sphere), NegotiationType: gen.NegotiationType(v.NegotiationType),
		Document: v.Document,
	}, nil
}

// --- Authoring (контракт наружу для generation, этап 05) ---

var _ Authoring = (*service)(nil)

// generationStatus — только поле status из scenarios.generation, без
// остального: нужен в двух не связанных друг с другом местах (SaveDraft —
// нельзя ли начать правку прямо сейчас, BeginGeneration — не идёт ли уже
// задание) и не стоит того, чтобы протаскивать через них весь снимок.
// Повреждённая generation — дефект самого generation (единственного
// писателя этого столбца), не пользовательский ввод; молчать о нём нельзя
// (правило 8), но и валить операцию из-за витринного поля не стоит — как
// уже решено для passportOrGeneration.
func generationStatus(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var g struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		log.Printf("scenarios: не удалось разобрать generation: %v", err)
		return ""
	}
	return g.Status
}

func (s *service) GenerationState(ctx context.Context, id uuid.UUID) (GenerationSnapshot, error) {
	row, err := s.store.get(ctx, s.pool, id)
	if errors.Is(err, errScenarioNotFound) {
		return GenerationSnapshot{}, errScenarioMissing()
	}
	if err != nil {
		return GenerationSnapshot{}, err
	}
	return GenerationSnapshot{
		State: row.Generation, Draft: row.DraftDocument,
		Mode: gen.Mode(row.Mode), Archived: row.ArchivedAt != nil,
	}, nil
}

// BeginGeneration — старт задания (D-05): архив, идущее задание и (для
// правки) отсутствие черновика — три независимых 409/404 на входе, в
// одной транзакции с самой записью, чтобы никто не проскочил в щель между
// проверкой и записью (та же гвардия SELECT … FOR UPDATE, что у SaveDraft
// и Publish).
func (s *service) BeginGeneration(ctx context.Context, id uuid.UUID, isEdit bool, state json.RawMessage) error {
	return pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.store.getForUpdate(ctx, tx, id)
		if errors.Is(err, errScenarioNotFound) {
			return errScenarioMissing()
		}
		if err != nil {
			return err
		}
		if row.ArchivedAt != nil {
			return errArchived()
		}
		if isEdit && row.DraftDocument == nil {
			return errDraftMissingForEdit()
		}
		if generationStatus(row.Generation) == "running" {
			return errGenerationRunning()
		}
		return s.store.setGeneration(ctx, tx, id, state)
	})
}

func (s *service) UpdateGeneration(ctx context.Context, id uuid.UUID, state json.RawMessage) error {
	return s.store.setGeneration(ctx, s.pool, id, state)
}

// FinishGeneration — конец задания: успех сохраняется тем же путём, что
// SaveDraft (Validate → draft_check, Fingerprint, draft_updated_*), одной
// транзакцией с generation; отличие от SaveDraft — здесь никогда не
// меняется scenarios.mode (документ авторства уже приведён к нему самим
// generation до вызова, «решения по умолчанию» плана этапа 05) и не
// блокируется схемными ошибками (FR-SC-15 — про ручное сохранение;
// результат генерации сохраняется с диагностикой для правки в форме, даже
// если она осталась). Провал (document == nil) трогает только generation.
func (s *service) FinishGeneration(ctx context.Context, id uuid.UUID, state json.RawMessage, document json.RawMessage, actorID uuid.UUID) error {
	if document == nil {
		return s.store.setGeneration(ctx, s.pool, id, state)
	}

	diags := scenariodoc.Validate(document)
	fingerprint, err := scenariodoc.Fingerprint(document)
	if err != nil {
		return fmt.Errorf("отпечаток документа после генерации: %w", err)
	}
	tree, err := decodeTree(document)
	if err != nil {
		return fmt.Errorf("разбор документа после генерации: %w", err)
	}
	slug := passportField(tree, "id")
	checkJSON, err := json.Marshal(toCheckResult(diags))
	if err != nil {
		return fmt.Errorf("сериализация результата проверки: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)

	return pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.store.getForUpdate(ctx, tx, id); err != nil {
			if errors.Is(err, errScenarioNotFound) {
				return errScenarioMissing()
			}
			return err
		}
		if err := s.store.saveDraft(ctx, tx, id, draftUpdate{
			Slug: slug, Mode: nil, DraftDocument: document, DraftFingerprint: fingerprint,
			DraftCheck: checkJSON, DraftUpdatedAt: now, UpdatedBy: actorID,
		}); err != nil {
			return err
		}
		return s.store.setGeneration(ctx, tx, id, state)
	})
}

func (s *service) FailRunningGenerations(ctx context.Context, message string) (int, error) {
	return s.store.failRunningGenerations(ctx, s.pool, message)
}

// --- работа с деревом документа ---

func decodeTree(document []byte) (map[string]any, error) {
	var tree map[string]any
	if err := json.Unmarshal(document, &tree); err != nil {
		return nil, fmt.Errorf("документ должен быть JSON-объектом: %w", err)
	}
	return tree, nil
}

func encodeTree(tree map[string]any) ([]byte, error) {
	return json.Marshal(tree)
}

// setPassport проставляет режим, происхождение и (если задано) название в
// паспорте документа — общий шаг создания сценария из шаблона, каркаса или
// копией (04-scenarios.md, задача «Создание»).
func setPassport(tree map[string]any, mode gen.Mode, origin gen.Origin, title *string) {
	passport, ok := tree["passport"].(map[string]any)
	if !ok || passport == nil {
		passport = map[string]any{}
	}
	passport["mode"] = string(mode)
	passport["origin"] = string(origin)
	if title != nil {
		if t := strings.TrimSpace(*title); t != "" {
			passport["title"] = t
		}
	}
	tree["passport"] = passport
}

func setPassportVersion(tree map[string]any, number int) {
	if p, ok := tree["passport"].(map[string]any); ok {
		p["version"] = number
	}
}

// setAuthoring заменяет только перечисленные ключи authoring, остальные
// (`brief`, `notes`) — как в источнике документа.
func setAuthoring(tree map[string]any, fields map[string]any) {
	auth, ok := tree["authoring"].(map[string]any)
	if !ok || auth == nil {
		auth = map[string]any{}
	}
	for k, v := range fields {
		auth[k] = v
	}
	tree["authoring"] = auth
}

func passportField(tree map[string]any, key string) string {
	if p, ok := tree["passport"].(map[string]any); ok {
		if v, ok := p[key].(string); ok {
			return v
		}
	}
	return ""
}

func topField(tree map[string]any, key string) string {
	if v, ok := tree[key].(string); ok {
		return v
	}
	return ""
}

// --- проверка документа ---

func toCheckResult(diags []scenariodoc.Diagnostic) gen.CheckResult {
	messages := make([]gen.CheckMessage, 0, len(diags))
	blocking := 0
	for _, d := range diags {
		msg := gen.CheckMessage{Message: d.Message, Path: d.Path, Severity: gen.Warning}
		if d.Severity == scenariodoc.SeverityError {
			msg.Severity = gen.Blocking
			blocking++
		}
		if d.Rule != "" {
			rule := d.Rule
			msg.Rule = &rule
		}
		if d.Level != "" {
			level := gen.Difficulty(d.Level)
			msg.Difficulty = &level
		}
		messages = append(messages, msg)
	}
	return gen.CheckResult{Blocking: blocking, EngineVersion: scenariodoc.EngineVersion, Messages: messages}
}

func decodeCheckResult(raw json.RawMessage) (gen.CheckResult, error) {
	if raw == nil {
		return gen.CheckResult{EngineVersion: scenariodoc.EngineVersion, Messages: []gen.CheckMessage{}}, nil
	}
	var cr gen.CheckResult
	if err := json.Unmarshal(raw, &cr); err != nil {
		return gen.CheckResult{}, fmt.Errorf("разбор результата проверки: %w", err)
	}
	if cr.Messages == nil {
		cr.Messages = []gen.CheckMessage{}
	}
	return cr, nil
}

func toFieldErrors(diags []scenariodoc.Diagnostic, keep func(scenariodoc.Diagnostic) bool) []gen.FieldError {
	var out []gen.FieldError
	for _, d := range diags {
		if keep(d) {
			out = append(out, gen.FieldError{Path: d.Path, Message: d.Message})
		}
	}
	return out
}

func isSchemaError(d scenariodoc.Diagnostic) bool {
	return d.Severity == scenariodoc.SeverityError && d.Rule == "schema"
}

func isBlockingError(d scenariodoc.Diagnostic) bool {
	return d.Severity == scenariodoc.SeverityError
}

func hasSchemaError(diags []scenariodoc.Diagnostic) bool {
	for _, d := range diags {
		if isSchemaError(d) {
			return true
		}
	}
	return false
}

// draftETag — метка черновика для If-Match/ETag (D-28): микросекунды
// draft_updated_at в кавычках. Отпечаток документа для этого не годится —
// правка тегов и заметок его не меняет (04-scenarios.md, «Ловушки»), а
// конкурентную правку ловить нужно на любом изменении черновика.
func draftETag(t *time.Time) string {
	if t == nil {
		return `""`
	}
	return `"` + strconv.FormatInt(t.UnixMicro(), 10) + `"`
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

type service struct {
	pool      *pgxpool.Pool
	store     *store
	audit     audit.Writer
	masterKey []byte
	checker   ProfileChecker
	sessions  SessionCounter
	now       func() time.Time
}

func newService(pool *pgxpool.Pool, auditWriter audit.Writer, masterKey []byte, checker ProfileChecker, sessions SessionCounter) *service {
	return &service{
		pool: pool, store: newStore(), audit: auditWriter, masterKey: masterKey,
		checker: checker, sessions: sessions, now: time.Now,
	}
}

// view — профиль в ответе: для администратора — adminRow с дополнениями,
// для остальных — только publicRow из представления (FR-PF-04). Ровно
// одно поле не nil.
type view struct {
	Admin  *adminView
	Public *publicRow
}

type adminView struct {
	adminRow
	RunningSessions *int
	SameKeyProfiles []uuid.UUID
}

// KeysInput — тело PUT …/keys: Set = поле пришло; Value nil при Set —
// удалить ключ (D-43).
type KeysInput struct {
	OpenRouterSet    bool
	OpenRouter       *string
	ModelServerSet   bool
	ModelServerToken *string
}

func fieldError(path, message string) *httpx.Error {
	return httpx.NewError(httpx.KindValidationFailed, message).
		WithErrors([]gen.FieldError{{Path: path, Message: message}})
}

func errMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Такого профиля тренажёра нет.")
}

func mapError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errMissing()
	case errors.Is(err, errNameTaken):
		return fieldError("/name", "Профиль с таким названием уже есть.")
	}
	return err
}

// --- чтение ---

// List — не-администратору только из trainer_profiles_public; фильтр по
// последним символам ключа ему запрещён: по составу ответа их можно было
// бы подобрать (arena-api.yaml, listTrainerProfiles).
func (s *service) List(ctx context.Context, a actor.Actor, includeArchived bool, keyLast4 *string) ([]view, error) {
	if !a.IsAdmin() {
		if keyLast4 != nil {
			return nil, httpx.NewError(httpx.KindForbiddenRole, "Искать профили по последним символам ключа может только администратор.")
		}
		rows, err := s.store.listPublic(ctx, s.pool, includeArchived)
		if err != nil {
			return nil, err
		}
		out := make([]view, 0, len(rows))
		for i := range rows {
			out = append(out, view{Public: &rows[i]})
		}
		return out, nil
	}
	rows, err := s.store.listAdmin(ctx, s.pool, includeArchived, keyLast4)
	if err != nil {
		return nil, err
	}
	out := make([]view, 0, len(rows))
	for i := range rows {
		out = append(out, view{Admin: &adminView{adminRow: rows[i]}})
	}
	return out, nil
}

func (s *service) Get(ctx context.Context, a actor.Actor, id uuid.UUID) (view, error) {
	if !a.IsAdmin() {
		r, err := s.store.getPublic(ctx, s.pool, id)
		if err != nil {
			return view{}, mapError(err)
		}
		return view{Public: &r}, nil
	}
	v, err := s.adminView(ctx, s.pool, id)
	if err != nil {
		return view{}, mapError(err)
	}
	return view{Admin: &v}, nil
}

// adminView — профиль для администратора с числом идущих сессий и
// профилями с тем же ключом.
func (s *service) adminView(ctx context.Context, q querier, id uuid.UUID) (adminView, error) {
	r, err := s.store.getAdmin(ctx, q, id, false)
	if err != nil {
		return adminView{}, err
	}
	running, err := s.sessions.RunningByProfile(ctx, id)
	if err != nil {
		return adminView{}, err
	}
	same, err := s.store.sameKeyProfiles(ctx, q, id, r.OpenRouterKeyLast4, r.ModelServerTokenLast4)
	if err != nil {
		return adminView{}, err
	}
	return adminView{adminRow: r, RunningSessions: &running, SameKeyProfiles: same}, nil
}

// --- запись ---

// Create — новый профиль или копия. Копия переносит настройки, но не
// ключи: их задают отдельно (PUT …/keys). settings nil — поле не
// пришло: берутся настройки копируемого профиля или пустые, и всё
// наследуется из профиля по умолчанию.
func (s *service) Create(ctx context.Context, a actor.Actor, name string, copyFrom *uuid.UUID, settings json.RawMessage) (view, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return view{}, fieldError("/name", "Укажите название профиля.")
	}
	var out adminView
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		own := []byte(settings)
		if own == nil && copyFrom != nil {
			src, err := s.store.getAdmin(ctx, tx, *copyFrom, false)
			if errors.Is(err, ErrNotFound) {
				return fieldError("/copy_from", "Копируемого профиля нет.")
			}
			if err != nil {
				return err
			}
			own = src.Settings
		}
		if own == nil {
			own = []byte(`{}`)
		}
		if err := s.validate(ctx, tx, false, own); err != nil {
			return err
		}
		id, err := s.store.insert(ctx, tx, name, false, own, &a.UserID)
		if err != nil {
			return mapError(err)
		}
		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionProfileSaved, Outcome: audit.OutcomeOK,
			Details: map[string]any{"profile_id": id, "created": true, "copied": copyFrom != nil},
		}); err != nil {
			return err
		}
		out, err = s.adminView(ctx, tx, id)
		return err
	})
	if err != nil {
		return view{}, err
	}
	return view{Admin: &out}, nil
}

// Update — правка названия и настроек. settings заменяет настройки
// профиля целиком (D-43); ревизия +1, действует со следующего старта —
// идущие сессии доигрывают на своём снимке (FR-PF-03).
func (s *service) Update(ctx context.Context, a actor.Actor, id uuid.UUID, name *string, settings json.RawMessage) (view, error) {
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return view{}, fieldError("/name", "Укажите название профиля.")
		}
		name = &trimmed
	}
	var out adminView
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.store.getAdmin(ctx, tx, id, true)
		if err != nil {
			return mapError(err)
		}
		if cur.ArchivedAt != nil {
			return httpx.NewError(httpx.KindArchived, "Профиль в архиве — его нельзя изменить.")
		}
		if name == nil && settings == nil {
			out, err = s.adminView(ctx, tx, id)
			return err
		}
		if settings != nil {
			if err := s.validate(ctx, tx, cur.IsDefault, settings); err != nil {
				return err
			}
		}
		if err := s.store.update(ctx, tx, id, name, settings, a.UserID); err != nil {
			return mapError(err)
		}
		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionProfileSaved, Outcome: audit.OutcomeOK,
			Details: map[string]any{
				"profile_id": id, "name_changed": name != nil, "settings_changed": settings != nil,
				"revision": cur.Revision + 1,
			},
		}); err != nil {
			return err
		}
		out, err = s.adminView(ctx, tx, id)
		return err
	})
	if err != nil {
		return view{}, err
	}
	return view{Admin: &out}, nil
}

// validate — UC-A-01 по итоговым настройкам: у профиля по умолчанию —
// по его собственным (наследовать ему не из чего) и по итоговым всех его
// наследников, у остальных — после слияния с профилем по умолчанию.
func (s *service) validate(ctx context.Context, q querier, isDefault bool, own []byte) error {
	ownMap, err := parseSettings(own)
	if err != nil {
		return err
	}
	base := map[string]any{}
	if !isDefault {
		_, baseRaw, err := s.store.defaultProfile(ctx, q)
		if err != nil && !errors.Is(err, errNoDefault) {
			return err
		}
		if base, err = parseSettings(baseRaw); err != nil {
			return err
		}
	}
	if errs := validateEffective(merge(base, ownMap)); len(errs) > 0 {
		return httpx.NewError(httpx.KindValidationFailed, errs[0].Message).WithErrors(errs)
	}
	if isDefault {
		return s.validateHeirs(ctx, q, ownMap)
	}
	return nil
}

// validateHeirs — правка профиля по умолчанию меняет итоговые настройки
// всех, кто из него наследует: каждый действующий профиль должен остаться
// годным (UC-A-01), иначе негодный снимок уйдёт клиенту-тренажёру.
func (s *service) validateHeirs(ctx context.Context, q querier, base map[string]any) error {
	rows, err := s.store.listAdmin(ctx, q, false, nil)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.IsDefault {
			continue
		}
		own, err := parseSettings(r.Settings)
		if err != nil {
			return err
		}
		if errs := validateEffective(merge(base, own)); len(errs) > 0 {
			msg := fmt.Sprintf("После этой правки профиль «%s» перестанет работать: %s", r.Name, errs[0].Message)
			return httpx.NewError(httpx.KindValidationFailed, msg).
				WithErrors([]gen.FieldError{{Path: "/settings", Message: msg}})
		}
	}
	return nil
}

// ReplaceKeys — только запись ключей (UC-A-03): в базу — шифр мастер-ключом
// и последние 4 символа, в журнал — только то, что ключ задан или удалён,
// без самого ключа и без его символов (D-44).
func (s *service) ReplaceKeys(ctx context.Context, a actor.Actor, id uuid.UUID, in KeysInput) (view, error) {
	openRouter, err := s.keyChange(in.OpenRouterSet, in.OpenRouter, "/openrouter_key")
	if err != nil {
		return view{}, err
	}
	token, err := s.keyChange(in.ModelServerSet, in.ModelServerToken, "/model_server_token")
	if err != nil {
		return view{}, err
	}
	if !openRouter.Set && !token.Set {
		return view{}, httpx.NewError(httpx.KindValidationFailed, "Передайте ключ OpenRouter или токен нашего сервера.")
	}

	var out adminView
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.store.getAdmin(ctx, tx, id, true); err != nil {
			return mapError(err)
		}
		if err := s.store.updateKeys(ctx, tx, id, openRouter, token, a.UserID); err != nil {
			return err
		}
		details := map[string]any{"profile_id": id}
		if openRouter.Set {
			details["openrouter_key"] = keyAction(openRouter)
		}
		if token.Set {
			details["model_server_token"] = keyAction(token)
		}
		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionProfileKeyChanged, Outcome: audit.OutcomeOK,
			Details: details,
		}); err != nil {
			return err
		}
		out, err = s.adminView(ctx, tx, id)
		return err
	})
	if err != nil {
		return view{}, err
	}
	return view{Admin: &out}, nil
}

func (s *service) keyChange(set bool, value *string, path string) (keyChange, error) {
	if !set {
		return keyChange{}, nil
	}
	if value == nil {
		return keyChange{Set: true}, nil
	}
	key := strings.TrimSpace(*value)
	if len([]rune(key)) < 8 {
		return keyChange{}, fieldError(path, "Ключ слишком короткий — проверьте, что он скопирован целиком.")
	}
	enc, err := crypto.Encrypt(s.masterKey, []byte(key))
	if err != nil {
		return keyChange{}, fmt.Errorf("шифрование ключа провайдера: %w", err)
	}
	l4 := last4(key)
	return keyChange{Set: true, Enc: enc, Last4: &l4}, nil
}

func keyAction(c keyChange) string {
	if c.Enc == nil {
		return "removed"
	}
	return "set"
}

// Archive — удаления профилей нет; профиль по умолчанию не архивируется
// никогда (FR-PF-05, ограничение trainer_profiles_default_not_archived).
func (s *service) Archive(ctx context.Context, a actor.Actor, id uuid.UUID) (view, error) {
	var out adminView
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.store.getAdmin(ctx, tx, id, true)
		if err != nil {
			return mapError(err)
		}
		if cur.IsDefault {
			return httpx.NewError(httpx.KindArchived, "Профиль по умолчанию нельзя архивировать: без него не обойдутся демо и назначения без своего профиля.")
		}
		if cur.ArchivedAt != nil {
			return httpx.NewError(httpx.KindArchived, "Профиль уже в архиве.")
		}
		if err := s.store.archive(ctx, tx, id, a.UserID); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionProfileSaved, Outcome: audit.OutcomeOK,
			Details: map[string]any{"profile_id": id, "archived": true},
		}); err != nil {
			return err
		}
		out, err = s.adminView(ctx, tx, id)
		return err
	})
	if err != nil {
		return view{}, err
	}
	return view{Admin: &out}, nil
}

// Check — «Проверить профиль» (UC-A-02), не дольше 20 секунд.
func (s *service) Check(ctx context.Context, id uuid.UUID) (time.Time, []CheckLine, error) {
	k, err := s.withKeys(ctx, id)
	if err != nil {
		return time.Time{}, nil, mapError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	lines, err := s.checker.Check(ctx, ProfileWithKeys{Effective: k.Effective, OpenRouterKey: k.openRouterKey, ModelServerToken: k.modelServerToken})
	if err != nil {
		return time.Time{}, nil, err
	}
	return s.now(), lines, nil
}

// --- Service ---

func (s *service) DefaultID(ctx context.Context) (uuid.UUID, error) {
	id, _, err := s.store.defaultProfile(ctx, s.pool)
	if errors.Is(err, errNoDefault) {
		return uuid.Nil, fmt.Errorf("в портале нет профиля тренажёра по умолчанию — он заводится при старте: %w", err)
	}
	return id, err
}

func (s *service) Effective(ctx context.Context, id uuid.UUID) (Effective, error) {
	eff, _, err := s.effective(ctx, s.pool, id)
	return eff, err
}

func (s *service) effective(ctx context.Context, q querier, id uuid.UUID) (Effective, map[string]any, error) {
	r, err := s.store.getAdmin(ctx, q, id, false)
	if err != nil {
		return Effective{}, nil, err
	}
	own, err := parseSettings(r.Settings)
	if err != nil {
		return Effective{}, nil, err
	}
	base := map[string]any{}
	if !r.IsDefault {
		_, baseRaw, err := s.store.defaultProfile(ctx, q)
		if err != nil && !errors.Is(err, errNoDefault) {
			return Effective{}, nil, err
		}
		if base, err = parseSettings(baseRaw); err != nil {
			return Effective{}, nil, err
		}
	}
	m := merge(base, own)
	settings, err := toGen(m)
	if err != nil {
		return Effective{}, nil, err
	}
	note, _ := m["model_providers_note"].(string)
	return Effective{
		ID: r.ID, Revision: r.Revision, Archived: r.ArchivedAt != nil,
		Settings: settings, Routes: routesOf(m), ModelProvidersNote: strings.TrimSpace(note),
	}, m, nil
}

// withKeys — итоговые настройки и расшифрованные ключи. Ключ отдаётся
// только тогда, когда в итоговых настройках есть и маршрут к нему.
func (s *service) withKeys(ctx context.Context, id uuid.UUID) (keyed, error) {
	eff, m, err := s.effective(ctx, s.pool, id)
	if err != nil {
		return keyed{}, err
	}
	enc, err := s.store.keys(ctx, s.pool, id)
	if err != nil {
		return keyed{}, err
	}
	out := keyed{Effective: eff, settings: m}
	if eff.Routes.OpenRouter && enc.OpenRouter != nil {
		if out.openRouterKey, err = s.decrypt(enc.OpenRouter); err != nil {
			return keyed{}, err
		}
	}
	if eff.Routes.OwnServer && enc.ModelServerToken != nil {
		if out.modelServerToken, err = s.decrypt(enc.ModelServerToken); err != nil {
			return keyed{}, err
		}
	}
	return out, nil
}

// keyed — итоговый профиль вместе с картой настроек и расшифрованными
// ключами; живёт только внутри службы.
type keyed struct {
	Effective
	settings         map[string]any
	openRouterKey    *string
	modelServerToken *string
}

func (s *service) decrypt(enc []byte) (*string, error) {
	plain, err := crypto.Decrypt(s.masterKey, enc)
	if err != nil {
		return nil, fmt.Errorf("расшифровка ключа провайдера (мастер-ключ сменился?): %w", err)
	}
	key := string(plain)
	return &key, nil
}

// SnapshotWithKeys — снимок для клиента-тренажёра (I-9). Без согласия на
// внешнюю нейросеть — ни блока openrouter в настройках, ни ключа
// OpenRouter (FR-AC-07, NFR-S-01).
func (s *service) SnapshotWithKeys(ctx context.Context, id uuid.UUID, externalAIAllowed bool) (Snapshot, error) {
	k, err := s.withKeys(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		ProfileID: k.ID, Revision: k.Revision, Settings: k.Settings,
		Routes: k.Routes, OpenRouterKey: k.openRouterKey, ModelServerToken: k.modelServerToken,
	}
	if !externalAIAllowed {
		if snap.Settings, err = toGen(stripOpenRouter(k.settings)); err != nil {
			return Snapshot{}, err
		}
		snap.Routes.OpenRouter = false
		snap.OpenRouterKey = nil
	}
	return snap, nil
}

func (s *service) ForClient(ctx context.Context, id uuid.UUID, externalAIAllowed bool) (Snapshot, error) {
	eff, m, err := s.effective(ctx, s.pool, id)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{ProfileID: eff.ID, Revision: eff.Revision, Settings: eff.Settings, Routes: eff.Routes}
	if !externalAIAllowed {
		if snap.Settings, err = toGen(stripOpenRouter(m)); err != nil {
			return Snapshot{}, err
		}
		snap.Routes.OpenRouter = false
	}
	return snap, nil
}

func (s *service) Names(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.names(ctx, s.pool, ids)
}

// --- Provisioner ---

// EnsureDefaultTx — профиль по умолчанию, если его ещё нет (D-46).
// Создание пишется в журнал от имени системы.
func (s *service) EnsureDefaultTx(ctx context.Context, tx pgx.Tx, name string, settings json.RawMessage) (bool, error) {
	_, _, err := s.store.defaultProfile(ctx, tx)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, errNoDefault) {
		return false, err
	}
	if err := s.validate(ctx, tx, true, settings); err != nil {
		return false, fmt.Errorf("профиль по умолчанию из db/seed/base.json не проходит проверку: %w", err)
	}
	id, err := s.store.insert(ctx, tx, name, true, settings, nil)
	if errors.Is(err, errNameTaken) {
		return false, fmt.Errorf("не завести профиль по умолчанию: название «%s» уже занято другим профилем", name)
	}
	if err != nil {
		return false, err
	}
	if err := s.audit.Write(ctx, tx, audit.Entry{
		ActorKind: audit.ActorSystem, Action: gen.AuditActionProfileSaved, Outcome: audit.OutcomeOK,
		Details: map[string]any{"profile_id": id, "created": true, "default": true},
	}); err != nil {
		return false, err
	}
	return true, nil
}

var (
	_ Service     = (*service)(nil)
	_ Provisioner = (*service)(nil)
)

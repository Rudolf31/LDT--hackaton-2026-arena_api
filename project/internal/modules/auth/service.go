package auth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
	"arena-portal-backend/internal/platform/ratelimit"
)

// roleDenialActions — под каким действием журнала пишется отказ по роли:
// тем, что операция записала бы при успехе. Чтения без своего действия
// (списки, журнал) при отказе по роли в журнал не попадают — записать их
// не под чем, а схему ради этого не меняем. Модули следующих этапов
// дописывают сюда свои операции.
var roleDenialActions = map[string]gen.AuditAction{
	"CreateUser":           gen.AuditActionUserSaved,
	"UpdateUser":           gen.AuditActionUserSaved,
	"SetUserGroupAccess":   gen.AuditActionAccessGranted,
	"CreateGroup":          gen.AuditActionGroupSaved,
	"UpdateGroup":          gen.AuditActionGroupSaved,
	"CreatePerson":         gen.AuditActionPersonSaved,
	"UpdatePerson":         gen.AuditActionPersonSaved,
	"WithdrawConsent":      gen.AuditActionConsentWithdrawn,
	"ExportAuditLog":       gen.AuditActionAuditExported,
	"UpdatePortalSettings": gen.AuditActionSettingsChanged,
	"PublishScenario":      gen.AuditActionScenarioPublished,
	"ArchiveScenario":      gen.AuditActionScenarioArchived,
	"CreateAssignments":    gen.AuditActionAssignmentCreated,
	"ReissueCodes":         gen.AuditActionCodeReissued,
	"ExtendAssignment":     gen.AuditActionAssignmentExtended,
	"CancelAssignment":     gen.AuditActionAssignmentCancelled,
	"UnblockCode":          gen.AuditActionCodeUnblocked,
}

type service struct {
	pool      *pgxpool.Pool
	store     *store
	audit     audit.Writer
	limiter   *ratelimit.Limiter
	dummyHash string
}

func newService(pool *pgxpool.Pool, auditWriter audit.Writer, loginLimiter *ratelimit.Limiter) (*service, error) {
	// Хеш-пустышка: сверка пароля неизвестного логина занимает столько же,
	// сколько настоящая, и время ответа не выдаёт, какие логины существуют.
	dummy, err := crypto.HashPassword("arena-dummy-password")
	if err != nil {
		return nil, fmt.Errorf("подготовка входа: %w", err)
	}
	return &service{pool: pool, store: newStore(), audit: auditWriter, limiter: loginLimiter, dummyHash: dummy}, nil
}

func errInvalidCredentials() *httpx.Error {
	return httpx.NewError(httpx.KindUnauthenticated, "Неверный логин или пароль.")
}

func errUserMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Такого пользователя нет.")
}

func fieldError(path, message string) *httpx.Error {
	return httpx.NewError(httpx.KindValidationFailed, message).
		WithErrors([]gen.FieldError{{Path: path, Message: message}})
}

// Login — вход по логину и паролю. Ошибка всегда одна, и для отключённого
// пользователя тоже: ответ не подсказывает, что именно не так. Неудача
// пишется в журнал и сохраняется, хотя вход не состоялся.
func (s *service) Login(ctx context.Context, login, password, clientAddr string) (userRow, error) {
	if ok, retryAfter := s.limiter.Allow(clientAddr + "|" + login); !ok {
		return userRow{}, httpx.NewError(httpx.KindRateLimited, "Слишком много попыток входа. Подождите минуту.").
			WithRetryAfter(int(math.Ceil(retryAfter.Seconds())))
	}

	var result userRow
	var failed bool
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := s.store.userByLogin(ctx, tx, login)
		known := err == nil
		if err != nil && !errors.Is(err, errUserNotFound) {
			return err
		}

		hash := s.dummyHash
		if known {
			hash = u.PasswordHash
		}
		match, err := crypto.VerifyPassword(password, hash)
		if err != nil {
			return fmt.Errorf("сверка пароля: %w", err)
		}

		if !known || !match || !u.IsActive {
			failed = true
			entry := audit.Entry{ActorKind: audit.ActorSystem, Action: gen.AuditActionLoginFailed, Outcome: audit.OutcomeDenied}
			if known {
				entry.ActorKind, entry.ActorUserID = audit.ActorUser, &u.ID
				entry.Details = map[string]any{"reason": failureReason(match, u.IsActive)}
			} else {
				entry.Details = map[string]any{"reason": "unknown_login"}
			}
			return s.audit.Write(ctx, tx, entry)
		}

		if err := s.store.touchLogin(ctx, tx, u.ID); err != nil {
			return err
		}
		result = u
		return s.audit.Write(ctx, tx, audit.Entry{ActorKind: audit.ActorUser, ActorUserID: &u.ID, Action: gen.AuditActionLogin, Outcome: audit.OutcomeOK})
	})
	if err != nil {
		return userRow{}, err
	}
	if failed {
		return userRow{}, errInvalidCredentials()
	}
	return result, nil
}

func failureReason(passwordMatched, active bool) string {
	if !passwordMatched {
		return "wrong_password"
	}
	if !active {
		return "inactive"
	}
	return "unknown"
}

// ActiveUser — пользователь из базы на этом запросе. false — нет такого
// или вход отключён: тогда cookie больше ничего не значит.
func (s *service) ActiveUser(ctx context.Context, id uuid.UUID) (userRow, bool, error) {
	u, err := s.store.userByID(ctx, s.pool, id)
	if errors.Is(err, errUserNotFound) {
		return userRow{}, false, nil
	}
	if err != nil {
		return userRow{}, false, err
	}
	return u, u.IsActive, nil
}

func (s *service) User(ctx context.Context, id uuid.UUID) (userRow, error) {
	u, err := s.store.userByID(ctx, s.pool, id)
	if errors.Is(err, errUserNotFound) {
		return userRow{}, errUserMissing()
	}
	return u, err
}

func (s *service) ListUsers(ctx context.Context) ([]userRow, error) {
	return s.store.listUsers(ctx, s.pool)
}

func (s *service) CreateUser(ctx context.Context, u NewUser, actorID uuid.UUID) (userRow, error) {
	var id uuid.UUID
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.CreateUserTx(ctx, tx, u, &actorID)
		return err
	})
	if err != nil {
		return userRow{}, err
	}
	return s.User(ctx, id)
}

type UserChanges struct {
	FullName    *string
	Role        *gen.Role
	IsActive    *bool
	NewPassword *string
}

func (s *service) UpdateUser(ctx context.Context, id uuid.UUID, c UserChanges, actorID uuid.UUID) (userRow, error) {
	// Отключить себя или снять с себя роль администратора — значит запереть
	// портал: другого администратора может и не быть.
	if id == actorID && ((c.IsActive != nil && !*c.IsActive) || (c.Role != nil && *c.Role != gen.Admin)) {
		return userRow{}, httpx.NewError(httpx.KindValidationFailed, "Нельзя отключить себя или снять с себя роль администратора.")
	}

	upd := userUpdate{FullName: c.FullName, Role: c.Role, IsActive: c.IsActive}
	var fields []string
	if c.FullName != nil {
		fields = append(fields, "full_name")
	}
	if c.Role != nil {
		fields = append(fields, "role")
	}
	if c.IsActive != nil {
		fields = append(fields, "is_active")
	}
	if c.NewPassword != nil {
		hash, err := crypto.HashPassword(*c.NewPassword)
		if err != nil {
			return userRow{}, fmt.Errorf("хеш пароля: %w", err)
		}
		upd.PasswordHash = &hash
		fields = append(fields, "password")
	}

	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.store.updateUser(ctx, tx, id, upd); err != nil {
			return mapUserError(err)
		}
		details := map[string]any{"user_id": id.String(), "fields": fields}
		if c.Role != nil {
			details["role"] = string(*c.Role)
		}
		if c.IsActive != nil {
			details["is_active"] = *c.IsActive
		}
		return s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &actorID,
			Action: gen.AuditActionUserSaved, Outcome: audit.OutcomeOK, Details: details,
		})
	})
	if err != nil {
		return userRow{}, err
	}
	return s.User(ctx, id)
}

// SetGroupAccess — полный список групп пользователя: недостающие строки
// добавляются, лишние удаляются, каждая перемена — своя запись в журнале.
func (s *service) SetGroupAccess(ctx context.Context, userID uuid.UUID, groupIDs []uuid.UUID, actorID uuid.UUID) (userRow, error) {
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.store.userByID(ctx, tx, userID); err != nil {
			return mapUserError(err)
		}
		current, err := s.store.accessibleGroups(ctx, tx, userID)
		if err != nil {
			return err
		}
		for _, g := range current {
			if slices.Contains(groupIDs, g) {
				continue
			}
			if err := s.store.deleteAccess(ctx, tx, userID, g); err != nil {
				return err
			}
			if err := s.writeAccessChange(ctx, tx, gen.AuditActionAccessRevoked, userID, g, &actorID); err != nil {
				return err
			}
		}
		for _, g := range groupIDs {
			if slices.Contains(current, g) {
				continue
			}
			if err := s.GrantAccessTx(ctx, tx, userID, g, actorID, &actorID); err != nil {
				return err
			}
			current = append(current, g)
		}
		return nil
	})
	if err != nil {
		return userRow{}, err
	}
	return s.User(ctx, userID)
}

func (s *service) writeAccessChange(ctx context.Context, tx pgx.Tx, action gen.AuditAction, userID, groupID uuid.UUID, actorID *uuid.UUID) error {
	entry := audit.Entry{
		ActorKind: audit.ActorSystem, Action: action, Outcome: audit.OutcomeOK,
		GroupID: &groupID, Details: map[string]any{"user_id": userID.String()},
	}
	if actorID != nil {
		entry.ActorKind, entry.ActorUserID = audit.ActorUser, actorID
	}
	return s.audit.Write(ctx, tx, entry)
}

func mapUserError(err error) error {
	switch {
	case errors.Is(err, errUserNotFound):
		return errUserMissing()
	case errors.Is(err, errLoginTaken):
		return fieldError("/login", "Такой логин уже занят.")
	case errors.Is(err, errLoginFormat):
		return fieldError("/login", "Логин — от 3 до 64 знаков: строчные латинские буквы, цифры, точка, дефис, подчёркивание.")
	case errors.Is(err, errGroupNotFound):
		return fieldError("/group_ids", "Одной из указанных групп нет.")
	}
	return err
}

// --- Provisioner ---

func (s *service) HasUsers(ctx context.Context, tx pgx.Tx) (bool, error) {
	return s.store.hasUsers(ctx, tx)
}

func (s *service) CreateUserTx(ctx context.Context, tx pgx.Tx, u NewUser, actorID *uuid.UUID) (uuid.UUID, error) {
	hash, err := crypto.HashPassword(u.Password)
	if err != nil {
		return uuid.Nil, fmt.Errorf("хеш пароля: %w", err)
	}
	id, err := s.store.insertUser(ctx, tx, u, hash)
	if err != nil {
		return uuid.Nil, mapUserError(err)
	}
	entry := audit.Entry{
		ActorKind: audit.ActorSystem, Action: gen.AuditActionUserSaved, Outcome: audit.OutcomeOK,
		Details: map[string]any{"user_id": id.String(), "role": string(u.Role), "created": true},
	}
	if actorID != nil {
		entry.ActorKind, entry.ActorUserID = audit.ActorUser, actorID
	}
	if err := s.audit.Write(ctx, tx, entry); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s *service) GrantAccessTx(ctx context.Context, tx pgx.Tx, userID, groupID, grantedBy uuid.UUID, actorID *uuid.UUID) error {
	if err := s.store.insertAccess(ctx, tx, userID, groupID, grantedBy); err != nil {
		return mapUserError(err)
	}
	return s.writeAccessChange(ctx, tx, gen.AuditActionAccessGranted, userID, groupID, actorID)
}

// --- GroupAccess ---

func (s *service) HasAccess(ctx context.Context, userID, groupID uuid.UUID) (bool, error) {
	return s.store.hasAccess(ctx, s.pool, userID, groupID)
}

func (s *service) AccessibleGroups(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.accessibleGroups(ctx, s.pool, userID)
}

func (s *service) RecordDenial(ctx context.Context, d Denial) error {
	return pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		return s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &d.UserID,
			Action: d.Action, Outcome: audit.OutcomeDenied,
			GroupID: d.GroupID, SubjectID: d.SubjectID,
			Details: map[string]any{"operation": d.Operation, "reason": d.Reason},
		})
	})
}

// DenyRole пишет отказ по роли, если у операции есть своё действие в журнале.
func (s *service) DenyRole(ctx context.Context, userID uuid.UUID, operation string) error {
	action, ok := roleDenialActions[operation]
	if !ok {
		return nil
	}
	return s.RecordDenial(ctx, Denial{UserID: userID, Action: action, Operation: operation, Reason: DenialReasonRole})
}

// --- Directory ---

func (s *service) UserNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.userNames(ctx, s.pool, ids)
}

var (
	_ GroupAccess = (*service)(nil)
	_ Provisioner = (*service)(nil)
	_ Directory   = (*service)(nil)
)

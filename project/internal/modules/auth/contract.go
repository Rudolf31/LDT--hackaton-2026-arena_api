// Package auth — вход в портал, пользователи, роли и доступ к группам,
// токены клиента-тренажёра. Роль и доступ читаются из базы на каждом
// запросе (CLAUDE.md, правило 3); в cookie лежит только номер пользователя
// и срок (D-19).
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
)

// Denial — отказ в доступе для журнала (outcome = denied). Action —
// действие, которое операция записала бы при успехе; у чтений без своего
// действия — session_opened, то есть «попытка открыть результаты группы».
type Denial struct {
	UserID    uuid.UUID
	Action    gen.AuditAction
	Operation string
	Reason    string
	GroupID   *uuid.UUID
	SubjectID *uuid.UUID
}

const (
	DenialReasonRole  = "role"
	DenialReasonGroup = "group"
)

// GroupAccess — доступ пользователя портала к результатам групп (FR-AC-08).
// Нет доступа — 403, а не пустой список; отказ пишется в журнал
// RecordDenial, в собственной транзакции: транзакция отказанного действия
// откатывается вместе с ошибкой и унесла бы запись с собой.
type GroupAccess interface {
	HasAccess(ctx context.Context, userID, groupID uuid.UUID) (bool, error)
	AccessibleGroups(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	RecordDenial(ctx context.Context, d Denial) error
}

type NewUser struct {
	Login    string
	Password string
	FullName string
	Role     gen.Role
}

// Provisioner — заведение пользователей и доступа внутри чужой транзакции:
// нужно заливке демо-данных (D-23), которая собирает всё одной транзакцией.
// actorUserID nil — действие системы.
type Provisioner interface {
	HasUsers(ctx context.Context, tx pgx.Tx) (bool, error)
	CreateUserTx(ctx context.Context, tx pgx.Tx, u NewUser, actorUserID *uuid.UUID) (uuid.UUID, error)
	GrantAccessTx(ctx context.Context, tx pgx.Tx, userID, groupID, grantedBy uuid.UUID, actorUserID *uuid.UUID) error
}

// Directory — имена пользователей портала для экранов (журнал, «кто
// изменил»). Это сотрудники HR, не участники: их имена на экране
// допустимы, в журнал и лог они всё равно не пишутся.
type Directory interface {
	UserNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

type TrainerTokenKind string

const (
	TrainerTokenParticipant TrainerTokenKind = "participant"
	TrainerTokenGuest       TrainerTokenKind = "guest"
	TrainerTokenRehearsal   TrainerTokenKind = "rehearsal"
)

// TrainerClaims — содержимое токена клиента-тренажёра (arena-portal-hr.md
// 8.1, «Вход»). Права по нему всё равно проверяются по базе на каждом
// запросе — токен говорит, кто пришёл, а не что ему можно.
type TrainerClaims struct {
	Kind         TrainerTokenKind `json:"k"`
	AssignmentID *uuid.UUID       `json:"a,omitempty"`
	SubjectID    *uuid.UUID       `json:"s,omitempty"`
	CodeID       *uuid.UUID       `json:"c,omitempty"`
	DemoGuestID  *uuid.UUID       `json:"g,omitempty"`
	RehearsalID  *uuid.UUID       `json:"r,omitempty"`
	ExpiresAt    time.Time        `json:"e"`
}

var (
	ErrTokenInvalid = errors.New("токен клиента-тренажёра не распознан")
	ErrTokenExpired = errors.New("срок токена клиента-тренажёра истёк")
)

// TrainerTokens выпускает и проверяет подписанные токены участника, гостя
// демо и репетиции. Токен участника выдаёт вход по коду (sessions, этап
// 07); гостя и репетиции — этапы 11 и 10.
type TrainerTokens interface {
	Issue(c TrainerClaims) (string, error)
	Verify(token string) (TrainerClaims, error)
}

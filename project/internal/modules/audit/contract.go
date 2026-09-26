// Package audit — журнал только на добавление (NFR-S-03). Write вызывается
// внутри транзакции модуля-потребителя: у журнала нет своего соединения,
// запись уходит в той же транзакции, что и само действие (CLAUDE.md,
// правило 4; I-4). Адреса чтения и выгрузки появляются в этапе 02, вместе
// со входом — до тех пор смотреть в журнал некому.
package audit

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
)

type ActorKind string

const (
	ActorUser        ActorKind = "user"
	ActorParticipant ActorKind = "participant"
	ActorSystem      ActorKind = "system"
)

type Outcome string

const (
	OutcomeOK     Outcome = "ok"
	OutcomeDenied Outcome = "denied"
)

// Entry — одна строка журнала. Details не должен нести имён и текстов
// реплик (CLAUDE.md, правило 5; I-5) — store проверяет это защитно, но
// ответственность в первую очередь на вызывающем коде.
type Entry struct {
	ActorKind   ActorKind
	ActorUserID *uuid.UUID
	Action      gen.AuditAction
	Outcome     Outcome
	SubjectID   *uuid.UUID
	SessionID   *uuid.UUID
	GroupID     *uuid.UUID
	RowsCount   *int
	Details     map[string]any
}

// Writer — контракт audit наружу: единственное, что видят другие модули
// (CLAUDE.md, «Устройство модуля»). tx — транзакция вызывающего сценария
// использования, audit её не открывает и не закрывает.
type Writer interface {
	Write(ctx context.Context, tx pgx.Tx, entry Entry) error
}

// UserLookup и SubjectLookup — интерфейсы потребителя для экранов журнала:
// имена пользователей портала живут в auth, названия групп и номера
// участников — в people. К их таблицам audit сам не ходит.
type UserLookup interface {
	UserNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

type SubjectLookup interface {
	GroupNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	SubjectNumbers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	SubjectIDByNumber(ctx context.Context, number string) (uuid.UUID, bool, error)
}

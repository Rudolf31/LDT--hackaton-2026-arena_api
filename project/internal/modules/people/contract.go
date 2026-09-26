// Package people — группы сотрудников, обезличенные номера участников
// (subjects) и таблица «номер → человек» (people). Имя сотрудника живёт
// только здесь; остальной портал знает участника по номеру (NFR-PR-01).
package people

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrKeyDestroyed — ключ данных участника уничтожен отзывом согласия:
	// его тексты больше не прочитать, и это не ошибка, а состояние.
	ErrKeyDestroyed = errors.New("ключ данных участника уничтожен")
	// ErrSubjectNotFound — такого номера участника нет.
	ErrSubjectNotFound = errors.New("участник не найден")
)

// Service — то, что другим модулям нужно знать о человеке за номером.
type Service interface {
	// DataKey — ключ шифрования данных участника, уже распакованный
	// мастер-ключом, или ErrKeyDestroyed.
	DataKey(ctx context.Context, subjectID uuid.UUID) ([]byte, error)
	// DisplayName — ФИО или псевдоним для экранов с доступом; ok = false
	// после отзыва согласия (виден только номер).
	DisplayName(ctx context.Context, subjectID uuid.UUID) (name string, isPseudonym bool, ok bool, err error)
	// Person — то, что о человеке за номером нужно экрану согласия и
	// отметке о письменном согласии: группа, ФИО или псевдоним, табельный
	// номер, отозвано ли согласие на внешнюю нейросеть. После отзыва
	// согласия связи «номер → человек» нет — ErrKeyDestroyed; нет номера —
	// ErrSubjectNotFound.
	Person(ctx context.Context, subjectID uuid.UUID) (PersonFacts, error)
	// PersonRefs — номера и имена пачки участников одним запросом
	// (назначение на группу — до 500 человек). Неизвестный номер в карте
	// отсутствует; после отзыва согласия есть только номер.
	PersonRefs(ctx context.Context, subjectIDs []uuid.UUID) (map[uuid.UUID]PersonRef, error)
	// GroupProfile — профиль тренажёра, выбранный у группы; nil — у группы
	// своего профиля нет и действует профиль по умолчанию (FR-PF-02).
	GroupProfile(ctx context.Context, groupID uuid.UUID) (*uuid.UUID, error)
}

// PersonRef — участник для экранов назначений: номер и, пока связь
// «номер → человек» есть, ФИО или псевдоним и текущая группа. Имя — для
// экрана, не для журнала и лога (CLAUDE.md, правило 5).
type PersonRef struct {
	SubjectID   uuid.UUID
	Number      string
	Present     bool // false — согласие отозвано, связи с человеком нет
	GroupID     uuid.UUID
	DisplayName *string
	IsPseudonym bool
	HasFullName bool
}

// PersonFacts — сведения о сотруднике для других модулей. Имя отсюда идёт
// только в текст экрана и в HMAC показанного текста, но не в журнал и не
// в лог (CLAUDE.md, правило 5).
type PersonFacts struct {
	GroupID             uuid.UUID
	FullName            *string
	Pseudonym           *string
	PersonnelNo         *string
	ExternalAIWithdrawn bool
}

// AssignmentCanceller — интерфейс потребителя: отзыв согласия отменяет
// открытые назначения и коды участника в своей транзакции. Реализует
// модуль assignments (этап 07); до него собирается заглушка.
type AssignmentCanceller interface {
	CancelForSubject(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) (int, error)
}

type NewGroup struct {
	Name       string
	Department *string
}

type NewPerson struct {
	GroupID     uuid.UUID
	FullName    *string
	Pseudonym   *string
	PersonnelNo *string
	JobTitle    *string
}

// Provisioner — заведение групп и сотрудников внутри чужой транзакции
// (заливка демо-данных, D-23). actorUserID nil — действие системы.
type Provisioner interface {
	CreateGroupTx(ctx context.Context, tx pgx.Tx, g NewGroup, actorUserID *uuid.UUID) (uuid.UUID, error)
	CreatePersonTx(ctx context.Context, tx pgx.Tx, p NewPerson, actorUserID *uuid.UUID) (uuid.UUID, error)
}

// Directory — названия групп и номера участников для экранов журнала.
type Directory interface {
	GroupNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	SubjectNumbers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	SubjectIDByNumber(ctx context.Context, number string) (uuid.UUID, bool, error)
}

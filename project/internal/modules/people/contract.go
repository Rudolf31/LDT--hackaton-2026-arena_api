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

// Package consents — тексты согласий и уведомлений, выбор варианта А/Б/В по
// профилю тренажёра, подстановки, приём ответов участника и отметки HR о
// письменном согласии (FR-AC-06, FR-AC-07, FR-AC-13). Таблицы —
// consent_texts и consent_records, обе только на добавление.
package consents

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
)

// Audience — кому показывается экран согласия: участник, назначение,
// режим и профиль тренажёра. Демо — гость на демо-участнике: ФИО нет.
// Режим и профиль знает назначение (assignments, этап 07), поэтому
// consents получает их готовыми, а не ищет сам.
type Audience struct {
	SubjectID    uuid.UUID
	AssignmentID *uuid.UUID
	Mode         gen.Mode
	Demo         bool
	ProfileID    uuid.UUID
}

// TextShown — текст в том виде, в каком его увидит участник: подстановки
// уже сделаны, SHA-256 посчитан от этого текста.
type TextShown struct {
	Kind    gen.ConsentKind
	TextID  uuid.UUID
	Version string
	Title   string
	Body    string
	SHA256  string
	Answers []gen.ConsentAnswerValue
}

// Screen — экран «Согласие и уведомление». CanProceed = false — начать
// нельзя, причина в BlockedReason готовой русской фразой.
type Screen struct {
	Variant           gen.ConsentScreenVariant
	Texts             []TextShown
	CanProceed        bool
	BlockedReason     *string
	ExternalAIOffered bool
	OwnServer         bool
}

// Answer — ответ участника на один текст экрана.
type Answer struct {
	Kind        gen.ConsentKind
	Answer      gen.ConsentAnswerValue
	TextID      uuid.UUID
	ShownSHA256 string
}

// Record — запись consent_records для ответа API.
type Record struct {
	ID              uuid.UUID
	Kind            gen.ConsentKind
	Answer          gen.ConsentAnswerValue
	TextVersion     *string
	AnsweredAt      time.Time
	DocumentRef     *string
	DocumentChannel *string
	SignedOn        *time.Time
	ValidUntil      *time.Time
	RecordedByName  *string
}

// Outcome — итог приёма ответов: можно ли начинать и на каких записях
// основываться при старте (sessions.main_consent_id,
// external_ai_consent_id).
type Outcome struct {
	Records             []Record
	CanStart            bool
	ExternalAIAllowed   bool
	Message             *string
	MainConsentID       *uuid.UUID
	ExternalAIConsentID *uuid.UUID
}

// Service — контракт consents для sessions и demo.
type Service interface {
	TextsFor(ctx context.Context, a Audience) (Screen, error)
	Record(ctx context.Context, a Audience, answers []Answer) (Outcome, error)
	// Usable — последняя запись вида у участника годится как основание
	// для сессии: не отказ, у письменного — срок не истёк. Для экранных
	// видов assignmentID сужает поиск до ответа перед этим назначением.
	Usable(ctx context.Context, subjectID uuid.UUID, assignmentID *uuid.UUID, kind gen.ConsentKind) (uuid.UUID, bool, error)
	// Records — записи по номерам, для разбора consent_ids при старте
	// сессии; неизвестные номера в ответ не попадают.
	Records(ctx context.Context, ids []uuid.UUID) ([]RecordInfo, error)
}

// RecordInfo — запись согласия без текста: чья, перед каким назначением,
// какого вида и годится ли основанием для сессии (не отказ).
type RecordInfo struct {
	ID           uuid.UUID
	SubjectID    uuid.UUID
	AssignmentID *uuid.UUID
	Kind         gen.ConsentKind
	Usable       bool
}

// AudienceResolver — интерфейс потребителя: по участнику тренажёра из
// токена (actor.Trainer) найти назначение, режим и профиль. Реализует
// assignments/sessions в этапе 07; до него — заглушка «ещё не
// реализовано» (D-48).
type AudienceResolver interface {
	Resolve(ctx context.Context) (Audience, error)
}

// Provisioner — тексты версии 1 при базовой заливке (D-46): вид, у которого
// ещё нет ни одной версии, получает текст из db/seed/consents.
type Provisioner interface {
	EnsureTextsTx(ctx context.Context, tx pgx.Tx, texts []NewText) (created int, err error)
}

// NewText — версия текста для заливки.
type NewText struct {
	Kind    gen.ConsentKind
	Version string
	Body    string
}

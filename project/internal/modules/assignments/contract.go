// Package assignments — назначения версии сценария сотрудникам и коды
// доступа к ним (FR-AC-01, FR-AC-02, FR-AC-05, FR-AC-12). Код в открытом
// виде показывается один раз и нигде не хранится: в базе два HMAC —
// всего кода и селектора (первых четырёх символов). Статус назначения
// выводится из сессий, а не хранится (arena-portal-hr.md 10.4, D-52).
package assignments

import (
	"context"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/httpx"
)

// State — назначение глазами тренажёра: чьё оно, на какую версию и
// уровень, с каким профилем и группой (группа на момент назначения —
// по ней проверяется доступ к сессиям), и можно ли по нему начинать.
type State struct {
	AssignmentID uuid.UUID
	SubjectID    uuid.UUID
	GroupID      uuid.UUID
	VersionID    uuid.UUID
	ProfileID    uuid.UUID
	Difficulty   gen.Difficulty
	DueAt        time.Time
	CancelledAt  *time.Time
	// ActiveCodeID — действующий код назначения; nil — кода нет (отозван
	// вместе с назначением или отзывом согласия).
	ActiveCodeID *uuid.UUID
	CodeBlocked  bool
}

// Redeemed — вход по коду состоялся: назначение и код, по которому вошли.
type Redeemed struct {
	State
	CodeID uuid.UUID
}

// Entry — контракт для sessions: вход по коду и состояние назначения на
// каждом запросе тренажёра (права — по базе, а не по токену; архитектура
// 9.4).
type Entry interface {
	// Redeem — ввод кода (UC-P-01): поиск по селектору, сверка HMAC,
	// счёт неудачных попыток и блокировка на пороге из настроек. Ошибка —
	// готовая *httpx.Error (404/403/410).
	Redeem(ctx context.Context, code string) (Redeemed, error)
	// ForTrainer — назначение участника тренажёра; 404, если его нет.
	ForTrainer(ctx context.Context, assignmentID uuid.UUID) (State, error)
}

// StartError — можно ли по назначению начать новую сессию с токеном,
// выданным на код codeID. Идущая сессия доигрывается и без этого
// (архитектура 9.4), поэтому вызывающий проверяет её раньше.
func (s State) StartError(codeID *uuid.UUID, now time.Time) error {
	switch {
	case s.CancelledAt != nil:
		return errCancelled()
	case s.ActiveCodeID == nil || codeID == nil || *s.ActiveCodeID != *codeID:
		return errCodeRevoked()
	case s.CodeBlocked:
		return errCodeBlocked()
	case !now.Before(s.DueAt):
		return errExpired()
	}
	return nil
}

func errCancelled() *httpx.Error {
	return httpx.NewError(httpx.KindAssignmentCancelled, "Назначение отменено — обратитесь к HR.")
}

func errCodeRevoked() *httpx.Error {
	return httpx.NewError(httpx.KindCodeRevoked, "Этот код заменён новым — возьмите новый код у HR.")
}

func errCodeBlocked() *httpx.Error {
	return httpx.NewError(httpx.KindCodeBlocked, "Код заблокирован — обратитесь к HR.")
}

func errExpired() *httpx.Error {
	return httpx.NewError(httpx.KindAssignmentExpired, "Срок назначения истёк — обратитесь к HR.")
}

// SessionSummary — сессии одного назначения, из которых выводится статус
// (arena-portal-hr.md 10.4) и поля списка назначений.
type SessionSummary struct {
	Attempts      int
	RunningID     *uuid.UUID
	Finished      bool // есть сессия, дошедшая до итогового статуса, кроме «прервана»
	LastStatus    *gen.SessionStatus
	LastStartedAt *time.Time
	BreakStage    *string
	BreakTurn     *int
	Spans         []Span
}

// Span — время жизни одной сессии: End nil — идёт.
type Span struct {
	Start time.Time
	End   *time.Time
}

// RunningAt — шла ли какая-то сессия в момент t (running_session_at_cancel).
func (s SessionSummary) RunningAt(t time.Time) bool {
	for _, span := range s.Spans {
		if !span.Start.After(t) && (span.End == nil || span.End.After(t)) {
			return true
		}
	}
	return false
}

// SessionFacts — интерфейс потребителя: сводка сессий по назначениям.
// Реализует sessions; таблицу sessions assignments сам не читает
// (CLAUDE.md, «К чужим таблицам не ходим»).
type SessionFacts interface {
	Summaries(ctx context.Context, assignmentIDs []uuid.UUID) (map[uuid.UUID]SessionSummary, error)
}

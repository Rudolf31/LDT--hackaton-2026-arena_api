package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/modules/assignments"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
)

// Пересылки для зависимостей по кругу (people ↔ assignments, consents ↔
// sessions, assignments ↔ sessions, scenarios и profiles → sessions):
// модуль получает пересылку при сборке, а настоящую реализацию она
// узнаёт, когда собран второй модуль. Всё связывается в buildApp до
// первого запроса; пустая пересылка — ошибка сборки, а не паника.

var errNotWired = errors.New("зависимость модуля не связана при сборке портала")

type lateCanceller struct{ target people.AssignmentCanceller }

func (l *lateCanceller) CancelForSubject(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) (int, error) {
	if l.target == nil {
		return 0, errNotWired
	}
	return l.target.CancelForSubject(ctx, tx, subjectID)
}

type lateAudience struct{ target consents.AudienceResolver }

func (l *lateAudience) Resolve(ctx context.Context) (consents.Audience, error) {
	if l.target == nil {
		return consents.Audience{}, errNotWired
	}
	return l.target.Resolve(ctx)
}

type lateSessionFacts struct{ target assignments.SessionFacts }

func (l *lateSessionFacts) Summaries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]assignments.SessionSummary, error) {
	if l.target == nil {
		return nil, errNotWired
	}
	return l.target.Summaries(ctx, ids)
}

type lateVersionCounts struct {
	target scenarios.VersionSessionCounter
}

func (l *lateVersionCounts) Counts(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	if l.target == nil {
		return nil, errNotWired
	}
	return l.target.Counts(ctx, ids)
}

type lateRunning struct{ target profiles.SessionCounter }

func (l *lateRunning) RunningByProfile(ctx context.Context, profileID uuid.UUID) (int, error) {
	if l.target == nil {
		return 0, errNotWired
	}
	return l.target.RunningByProfile(ctx, profileID)
}

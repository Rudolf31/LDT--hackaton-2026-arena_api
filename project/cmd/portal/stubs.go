package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// noAssignmentsYet — отмена назначений при отзыве согласия до этапа 07:
// назначений ещё нет, отменять нечего. В 07 её заменяет модуль assignments.
type noAssignmentsYet struct{}

func (noAssignmentsYet) CancelForSubject(context.Context, pgx.Tx, uuid.UUID) (int, error) {
	return 0, nil
}

// noRehearsalsYet — счётчик репетиций до этапа 10: репетиций ещё нет,
// Admission.Done всегда 0 (D-06). В 10 её заменяет модуль rehearsals.
type noRehearsalsYet struct{}

func (noRehearsalsYet) Count(context.Context, uuid.UUID, string) (int, error) {
	return 0, nil
}

// noSessionsYet — число сессий на версии сценария до этапов 07/08: сессий
// ещё нет, VersionSummary.sessions_count всегда 0.
type noSessionsYet struct{}

func (noSessionsYet) Counts(context.Context, []uuid.UUID) (map[uuid.UUID]int, error) {
	return map[uuid.UUID]int{}, nil
}

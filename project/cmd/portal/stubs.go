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

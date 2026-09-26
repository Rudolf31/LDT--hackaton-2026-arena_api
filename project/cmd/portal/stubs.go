package main

import (
	"context"

	"github.com/google/uuid"
)

// noRehearsalsYet — счётчик репетиций до этапа 10: репетиций ещё нет,
// Admission.Done всегда 0 (D-06). В 10 её заменяет модуль rehearsals.
type noRehearsalsYet struct{}

func (noRehearsalsYet) Count(context.Context, uuid.UUID, string) (int, error) {
	return 0, nil
}

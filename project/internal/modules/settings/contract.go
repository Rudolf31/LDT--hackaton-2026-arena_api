// Package settings — единственная строка portal_settings: порог допуска к
// оценке по числу репетиций, таймаут брошенной сессии, порог блокировки
// кода после неудачных попыток. Демо-режим здесь только читается — он
// задаётся переменной окружения ARENA_DEMO, а не хранится в базе
// (CLAUDE.md, «Действующие решения»; arena-portal-hr.md 8.2, FR-AC-09).
package settings

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Settings struct {
	AdmissionRehearsals   int
	AbandonTimeoutMinutes int
	CodeMaxFailedAttempts int
	DemoMode              bool
	UpdatedAt             *time.Time
	UpdatedByName         *string
}

// Patch — то, что можно поправить через PATCH /api/portal/settings. Указатель
// nil значит «не менять это поле» (arena-api.yaml: PortalSettingsPatch,
// minProperties: 1).
type Patch struct {
	AdmissionRehearsals   *int
	AbandonTimeoutMinutes *int
	CodeMaxFailedAttempts *int
}

// Service — контракт settings наружу. Get понадобится другим модулям
// (scenarios — порог допуска, jobs — таймаут брошенной сессии, assignments —
// порог блокировки кода) на следующих этапах.
type Service interface {
	Get(ctx context.Context) (Settings, error)
	Update(ctx context.Context, patch Patch, actorUserID *uuid.UUID) (Settings, error)
}

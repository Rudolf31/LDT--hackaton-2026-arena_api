// Package jobs — фоновые задания портала. Пока одно: раз в минуту закрыть
// брошенные сессии (arena-portal-backend-architecture.md 7.3). Сброс
// зависших заданий авторства при старте делает сам generation (D-40).
package jobs

import (
	"context"
	"log/slog"
	"time"

	"arena-portal-backend/internal/modules/sessions"
	"arena-portal-backend/internal/modules/settings"
)

// interval — как часто задание смотрит на идущие сессии.
const interval = time.Minute

type Module struct {
	service *service
}

// New — startedAt — момент запуска процесса: время недоступности портала
// в таймаут брошенной сессии не входит (FR-ST-03, I-15). При нескольких
// копиях портала это правило придётся переписывать (архитектура 7.3).
func New(closer sessions.Closer, settings settings.Service, logger *slog.Logger, startedAt time.Time) *Module {
	return &Module{service: &service{closer: closer, settings: settings, logger: logger, startedAt: startedAt}}
}

// Run — тикер до отмены ctx (остановка процесса).
func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.service.tick(ctx)
		}
	}
}

// Tick — один проход задания; для тестов.
func (m *Module) Tick(ctx context.Context) (int, error) { return m.service.closeAbandoned(ctx) }

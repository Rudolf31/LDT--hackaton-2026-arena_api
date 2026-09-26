package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"arena-portal-backend/internal/modules/sessions"
	"arena-portal-backend/internal/modules/settings"
)

type service struct {
	closer    sessions.Closer
	settings  settings.Service
	logger    *slog.Logger
	startedAt time.Time
}

// tick — ошибка прохода не останавливает задание: она пишется в лог,
// следующий проход через минуту (NFR-R-03 — без молчаливого пропуска).
func (s *service) tick(ctx context.Context) {
	n, err := s.closeAbandoned(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("задание закрытия брошенных сессий не выполнилось, повторю через минуту", "ошибка", err)
		}
		return
	}
	if n > 0 {
		s.logger.Info("закрыты брошенные сессии", "число", n)
	}
}

func (s *service) closeAbandoned(ctx context.Context) (int, error) {
	cfg, err := s.settings.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("таймаут брошенной сессии из настроек: %w", err)
	}
	return s.closer.CloseAbandoned(ctx, s.startedAt, cfg.AbandonTimeoutMinutes)
}

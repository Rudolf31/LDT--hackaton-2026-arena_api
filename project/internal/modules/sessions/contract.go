package sessions

import (
	"context"
	"time"
)

// Closer — закрытие брошенных сессий для задания jobs (архитектура 7.3).
// portalStartedAt — момент запуска процесса: время недоступности портала
// в таймаут не входит (FR-ST-03, I-15). Возвращает число закрытых сессий.
type Closer interface {
	CloseAbandoned(ctx context.Context, portalStartedAt time.Time, timeoutMinutes int) (int, error)
}

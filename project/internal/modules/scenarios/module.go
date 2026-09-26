package scenarios

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/settings"
)

// Module — транспорт для адаптера (D-17) и контракт Versions для модулей
// следующих этапов.
type Module struct {
	Transport *Transport
	service   *service
}

// New собирает модуль. Имена автора черновика и публикатора читаются
// join'ом на portal_users прямо в store.go — как уже делает settings
// (internal/modules/settings/store.go) для updated_by_name — а не через
// отдельный контракт auth.Directory на один столбец. settingsSvc — порог
// допуска к оценке, справочно (D-26, архитектура 12.5); rehearsals и
// sessionCounts — заглушки до этапов 10 и 07/08 (cmd/portal/stubs.go).
func New(pool *pgxpool.Pool, auditWriter audit.Writer, settingsSvc settings.Service,
	rehearsals RehearsalCounter, sessionCounts VersionSessionCounter) *Module {
	svc := newService(pool, auditWriter, settingsSvc, rehearsals, sessionCounts)
	return &Module{Transport: &Transport{service: svc}, service: svc}
}

func (m *Module) Versions() Versions { return m.service }

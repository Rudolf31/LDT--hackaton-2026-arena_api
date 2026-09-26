package settings

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
)

// New собирает модуль settings: служба поверх пула и audit.Writer,
// транспорт поверх службы. cmd/portal/main.go хранит только возвращённый
// *Transport — с ним, по имени операции, и работает адаптер (D-17).
func New(pool *pgxpool.Pool, auditWriter audit.Writer, demoMode bool) *Transport {
	service := newService(pool, auditWriter, demoMode)
	return newTransport(service)
}

package people

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
)

type Module struct {
	Transport *Transport
	service   *service
}

// New собирает модуль. masterKey распаковывает ключи данных участников;
// canceller — отмена назначений при отзыве согласия (до этапа 07 — заглушка).
func New(pool *pgxpool.Pool, auditWriter audit.Writer, access auth.GroupAccess, canceller AssignmentCanceller, masterKey []byte) *Module {
	svc := newService(pool, auditWriter, access, canceller, masterKey)
	return &Module{Transport: &Transport{service: svc}, service: svc}
}

func (m *Module) Service() Service         { return m.service }
func (m *Module) Provisioner() Provisioner { return m.service }
func (m *Module) Directory() Directory     { return m.service }

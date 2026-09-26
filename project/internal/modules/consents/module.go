package consents

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
)

type Module struct {
	Transport *Transport
	service   *service
}

// New собирает модуль. audience — кто пришёл в тренажёр и с каким
// назначением: до этапа 07 — заглушка «ещё не реализовано» (D-48).
func New(pool *pgxpool.Pool, auditWriter audit.Writer, access auth.GroupAccess, staff people.Service,
	profileService profiles.Service, audience AudienceResolver,
) *Module {
	svc := newService(pool, auditWriter, access, staff, profileService)
	return &Module{Transport: &Transport{service: svc, audience: audience}, service: svc}
}

func (m *Module) Service() Service         { return m.service }
func (m *Module) Provisioner() Provisioner { return m.service }

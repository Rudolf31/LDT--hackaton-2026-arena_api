package profiles

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
)

type Module struct {
	Transport *Transport
	service   *service
}

// New собирает модуль. masterKey шифрует ключи провайдеров; checker —
// «Проверить профиль» (nil — заглушка «не проверялось», архитектура
// 10.3); sessions — число идущих сессий по профилю (до этапов 07/08 —
// заглушка).
func New(pool *pgxpool.Pool, auditWriter audit.Writer, masterKey []byte, checker ProfileChecker, sessions SessionCounter) *Module {
	if checker == nil {
		checker = StubChecker{}
	}
	svc := newService(pool, auditWriter, masterKey, checker, sessions)
	return &Module{Transport: &Transport{service: svc}, service: svc}
}

func (m *Module) Service() Service         { return m.service }
func (m *Module) Provisioner() Provisioner { return m.service }

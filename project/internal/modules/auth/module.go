package auth

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/ratelimit"
)

type Config struct {
	// HMACSecret — ARENA_CODE_HMAC_SECRET; ключи cookie и токенов
	// выводятся из него с разными метками (D-21).
	HMACSecret   []byte
	CookieSecure bool
	Demo         bool
}

// Module — транспорт для адаптера (D-17) и контракты для других модулей.
type Module struct {
	Transport *Transport
	service   *service
	tokens    *trainerTokens
}

// New собирает модуль. access — x-roles всех операций контракта
// (httpx.LoadOperationAccess).
func New(pool *pgxpool.Pool, auditWriter audit.Writer, cfg Config, loginLimiter *ratelimit.Limiter, access map[string]httpx.OperationAccess) (*Module, error) {
	svc, err := newService(pool, auditWriter, loginLimiter)
	if err != nil {
		return nil, err
	}
	return &Module{
		Transport: &Transport{service: svc, cookies: newSessionCodec(cfg.HMACSecret, cfg.CookieSecure), access: access, demo: cfg.Demo},
		service:   svc,
		tokens:    newTrainerTokens(cfg.HMACSecret),
	}, nil
}

func (m *Module) GroupAccess() GroupAccess     { return m.service }
func (m *Module) Provisioner() Provisioner     { return m.service }
func (m *Module) Directory() Directory         { return m.service }
func (m *Module) TrainerTokens() TrainerTokens { return m.tokens }

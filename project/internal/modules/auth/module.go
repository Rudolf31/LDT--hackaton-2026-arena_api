package auth

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/ratelimit"
)

// Limiters — пределы частоты, которые держит middleware (D-56): вход по
// коду — по адресу клиента, адреса тренажёра — по токену.
type Limiters struct {
	PortalLogin  *ratelimit.Limiter
	CodeAttempt  *ratelimit.Limiter
	TrainerToken *ratelimit.Limiter
}

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
func New(pool *pgxpool.Pool, auditWriter audit.Writer, cfg Config, limiters Limiters, access map[string]httpx.OperationAccess) (*Module, error) {
	svc, err := newService(pool, auditWriter, limiters.PortalLogin)
	if err != nil {
		return nil, err
	}
	tokens := newTrainerTokens(cfg.HMACSecret)
	return &Module{
		Transport: &Transport{
			service: svc, cookies: newSessionCodec(cfg.HMACSecret, cfg.CookieSecure), access: access, demo: cfg.Demo,
			tokens: tokens, codeLimiter: limiters.CodeAttempt, tokenLimiter: limiters.TrainerToken,
		},
		service: svc,
		tokens:  tokens,
	}, nil
}

func (m *Module) GroupAccess() GroupAccess     { return m.service }
func (m *Module) Provisioner() Provisioner     { return m.service }
func (m *Module) Directory() Directory         { return m.service }
func (m *Module) TrainerTokens() TrainerTokens { return m.tokens }

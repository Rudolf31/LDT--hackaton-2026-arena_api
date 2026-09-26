package generation

import (
	"context"
	"log/slog"

	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/platform/ai"
	"arena-portal-backend/internal/platform/ratelimit"
)

// Module — авторство сценария голосом и текстом (этап 05). GET /generation
// входит в контракт и вызывается адаптером cmd/portal/api.go через
// Transport.GetGeneration; остальные четыре адреса контракт не описывает
// (D-05) — cmd/portal/router.go монтирует их через методы Transport
// напрямую на chi, в обход строгого сервера.
type Module struct {
	Transport *Transport
	service   *service
}

// New собирает модуль. transcriber и generator — nil, если соответствующая
// модель не настроена (cfg.STTConfigured()/GenConfigured(), D-34/D-35):
// тогда адреса, которым она нужна, отвечают 503, а не падают на nil-указателе.
// baseCtx — контекст процесса: задание должно пережить ответ 202 на
// запустивший его запрос и быть отменено при остановке портала.
func New(scenariosAuthoring scenarios.Authoring, transcriber ai.Transcriber, generator ai.ScenarioGenerator,
	limiter *ratelimit.Limiter, logger *slog.Logger, baseCtx context.Context,
) *Module {
	svc := newService(scenariosAuthoring, transcriber, generator, logger, baseCtx)
	return &Module{Transport: &Transport{service: svc, limiter: limiter, logger: logger}, service: svc}
}

// Recover переводит задания, зависшие в running с прошлого запуска
// портала, в failed (arena-api.yaml, getGeneration; вызывается из
// cmd/portal/main.go до ListenAndServe).
func (m *Module) Recover(ctx context.Context) error {
	return m.service.recover(ctx)
}

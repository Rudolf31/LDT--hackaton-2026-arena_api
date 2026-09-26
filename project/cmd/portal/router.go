package main

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/httpx"
	arenalog "arena-portal-backend/internal/platform/log"
)

// buildRouter собирает HTTP-роутер: сгенерированный strict-сервер плюс
// серединное ПО в порядке из arena-portal-backend-architecture.md, «сборка
// API» (лимит размера → фильтр emotion и проверка по схеме → лог запроса →
// обработчик), плюс четыре ручных адреса авторства сценария (D-05).
//
// Доступ к сгенерированным операциям проверяет strict middleware модуля
// auth (accessMiddleware): оно знает имя операции и по нему — её x-roles в
// контракте. У четырёх ручных адресов записи в контракте нет — их роли
// (методолог, администратор — arena-portal-hr.md 8.2) передаются явно в
// portalMiddleware.
func buildRouter(a *api, accessMiddleware gen.StrictMiddlewareFunc,
	portalMiddleware func(operation string, roles ...string) func(http.Handler) http.Handler,
	bodySchemas *httpx.BodySchemas, logger *slog.Logger,
) http.Handler {
	strictHandler := gen.NewStrictHandlerWithOptions(a, []gen.StrictMiddlewareFunc{accessMiddleware}, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.HandleRequestError(logger),
		ResponseErrorHandlerFunc: httpx.HandleResponseError(logger),
	})

	baseRouter := chi.NewRouter()
	baseRouter.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, httpx.NewError(httpx.KindNotFound, "Такого адреса нет."))
	})
	baseRouter.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, httpx.NewError(httpx.KindNotFound, "Такого адреса нет."))
	})

	// Четыре ручных адреса авторства сценария голосом и текстом (D-05): их
	// нет в контракте, поэтому нет и в gen.StrictServerInterface — они
	// монтируются прямо на chi, в стороне от сгенерированного роутера и его
	// серединного ПО (свой лимит размера тела, своя проверка emotion —
	// internal/modules/generation/transport.go). Роли — методолог и
	// администратор (arena-portal-hr.md 8.2); отказ по роли на этих
	// адресах в журнал не пишется (в audit_action нет действия для
	// генерации — decisions.md, backlog.md).
	//
	// arenalog.RequestMiddleware — снаружи portalMiddleware: лог должен
	// увидеть запрос целиком, включая 401/403/429 самого middleware, а не
	// только то, что дошло до обработчика (chi применяет middleware в
	// порядке списка — первый элемент самый внешний).
	baseRouter.Group(func(r chi.Router) {
		roles := []string{httpx.RoleMethodologist, httpx.RoleAdmin}
		r.With(arenalog.RequestMiddleware(logger), portalMiddleware("GenerationAudio", roles...)).
			Post("/api/portal/scenarios/{scenarioId}/generation/audio", a.generation.PostGenerationAudio)
		r.With(arenalog.RequestMiddleware(logger), portalMiddleware("GenerationText", roles...)).
			Post("/api/portal/scenarios/{scenarioId}/generation/text", a.generation.PostGenerationText)
		r.With(arenalog.RequestMiddleware(logger), portalMiddleware("DraftVoiceEdit", roles...)).
			Post("/api/portal/scenarios/{scenarioId}/draft/voice-edit", a.generation.PostDraftVoiceEdit)
		r.With(arenalog.RequestMiddleware(logger), portalMiddleware("DraftTextEdit", roles...)).
			Post("/api/portal/scenarios/{scenarioId}/draft/text-edit", a.generation.PostDraftTextEdit)
	})

	// Middlewares оборачивают в обратном порядке (последний элемент —
	// самый внешний), поэтому httpx.Body идёт последним в списке: он
	// обязан отработать раньше лога запроса.
	return gen.HandlerWithOptions(strictHandler, gen.ChiServerOptions{
		BaseRouter: baseRouter,
		Middlewares: []gen.MiddlewareFunc{
			arenalog.RequestMiddleware(logger),
			httpx.Body(bodySchemas),
		},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("параметры запроса", "method", r.Method, "path", r.URL.Path, "error", err.Error())
			httpx.WriteError(w, httpx.NewError(httpx.KindInvalidBody, "Некорректные параметры запроса."))
		},
	})
}

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
// обработчик), плюс место для четырёх ручных адресов авторства (D-05).
//
// Доступ проверяет strict middleware модуля auth (accessMiddleware): оно
// знает имя операции и по нему — её x-roles в контракте.
func buildRouter(a *api, accessMiddleware gen.StrictMiddlewareFunc, bodySchemas *httpx.BodySchemas, logger *slog.Logger) http.Handler {
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
	// серединного ПО. Группа появится в этапе 05 (generation); здесь —
	// только место для неё.
	baseRouter.Group(func(r chi.Router) {
		_ = r // намеренно пусто до этапа 05
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

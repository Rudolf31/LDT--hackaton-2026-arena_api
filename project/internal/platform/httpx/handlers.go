package httpx

import (
	"errors"
	"log/slog"
	"net/http"
)

// HandleRequestError — RequestErrorHandlerFunc сгенерированного strict-сервера
// (разбор параметров запроса, тело, которое сам strict-обработчик не смог
// декодировать). Всегда problem+json по-русски — не текст генератора (I-8).
// Ветка без узнаваемого типа не отдаёт клиенту текст исходной ошибки: он может
// нести внутренние детали (см. HandleResponseError) и в любом случае не
// является готовой русской фразой (правило 8) — исходный текст уходит только
// в лог.
func HandleRequestError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var domainErr *Error
		if errors.As(err, &domainErr) {
			WriteError(w, domainErr)
			return
		}
		logger.Error("разбор запроса", "method", r.Method, "path", r.URL.Path, "error", err.Error())
		WriteError(w, NewError(KindInvalidBody, "Запрос не разобран."))
	}
}

// HandleResponseError — ResponseErrorHandlerFunc сгенерированного
// strict-сервера: сюда попадает любая ошибка, которую вернула служба
// модуля (включая httpx.NotImplemented()), и сбой самой записи ответа.
// Ветка без узнаваемого типа — не молчаливый отказ, а 500 с понятной
// русской фразой (NFR-R-03, I-8); текст исходной ошибки (например, обёрнутая
// ошибка pgx из store-слоя) в ответ клиенту не идёт — только в лог, чтобы не
// раскрывать внутренние детали портала.
func HandleResponseError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var domainErr *Error
		if errors.As(err, &domainErr) {
			WriteError(w, domainErr)
			return
		}
		logger.Error("обработка запроса", "method", r.Method, "path", r.URL.Path, "error", err.Error())
		WriteError(w, NewError(KindInternal, "Что-то пошло не так на сервере. Попробуйте ещё раз."))
	}
}

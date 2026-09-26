package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"arena-portal-backend/internal/api/gen"
)

// Body — серединное ПО для всех изменяющих запросов сгенерированного
// роутера, в этом порядке (arena-portal-backend-architecture.md,
// «сборка API»): лимит размера → фильтр emotion и проверка по схеме. Лог
// запроса и сам обработчик подключаются после него в cmd/portal/api.go.
//
// Тело разбирается один раз и кладётся обратно в r.Body, чтобы
// сгенерированный обработчик мог декодировать его в свой тип как обычно
// (D-04).
func Body(schemas *BodySchemas) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hasBody(r) {
				op, matched := schemas.match(r.Method, r.URL.Path)
				if matched && op.bodyRequired {
					WriteError(w, NewError(KindInvalidBody, "В запросе должно быть тело JSON."))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if ct := contentType(r); ct != "application/json" {
				WriteError(w, NewError(
					KindInvalidBody,
					"Изменяющие запросы принимаются только с телом application/json.",
				).WithDetail(fmt.Sprintf("получен Content-Type: %q", r.Header.Get("Content-Type"))))
				return
			}

			op, matched := schemas.match(r.Method, r.URL.Path)
			maxBytes := schemas.defaultMaxBytes
			if matched {
				maxBytes = op.maxBodyBytes
			}

			limited := http.MaxBytesReader(w, r.Body, maxBytes)
			raw, err := io.ReadAll(limited)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					WriteError(w, NewError(
						KindTooLarge,
						fmt.Sprintf("Тело запроса больше допустимого предела (%d байт).", maxBytes),
					))
					return
				}
				WriteError(w, NewError(KindInvalidBody, "Не удалось прочитать тело запроса."))
				return
			}

			var parsed any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&parsed); err != nil {
				WriteError(w, NewError(KindInvalidBody, "Тело запроса — не корректный JSON.").WithDetail(err.Error()))
				return
			}
			if dec.More() {
				WriteError(w, NewError(KindInvalidBody, "После тела запроса лишние данные."))
				return
			}

			// Фильтр emotion — до записи тела куда-либо, включая лог (I-1,
			// FR-RS-02). Именно здесь, до схемы: 400 за ключ emotion не
			// должен зависеть от того, закрыта схема или нет.
			if pointer, found := FindEmotionKey(parsed); found {
				WriteError(w, NewError(
					KindInvalidBody,
					"Полю с именем «emotion» в теле запроса взяться неоткуда — эмоций участника API не принимает.",
				).WithErrors([]gen.FieldError{{Path: pointer, Message: "ключ emotion недопустим на любой глубине тела"}}))
				return
			}

			if matched && op.schema != nil {
				if err := op.schema.Validate(parsed); err != nil {
					WriteError(w, NewError(
						KindInvalidBody,
						"Тело запроса не проходит проверку по схеме.",
					).WithErrors(fieldErrorsFromValidation(err)))
					return
				}
			}

			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			next.ServeHTTP(w, r)
		})
	}
}

// hasBody — тело в HTTP/1.1 сигнализируется либо явным Content-Length,
// либо chunked Transfer-Encoding; сам по себе Content-Type ничего не говорит
// о наличии тела (клиенты нередко ставят его по умолчанию на каждый запрос,
// включая GET/DELETE без тела).
func hasBody(r *http.Request) bool {
	return r.ContentLength > 0 || r.Header.Get("Transfer-Encoding") != ""
}

func contentType(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(strings.ToLower(ct))
}

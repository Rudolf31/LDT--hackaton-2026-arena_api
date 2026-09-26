// Package httpx — ответ application/problem+json (RFC 9457), разбор
// пагинации и фильтров, проверка тела по схеме контракта, отклонение ключа
// emotion, ограничение размера тела (arena-portal-backend-architecture.md 3.2).
package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"

	"arena-portal-backend/internal/api/gen"
)

// Kind — машинный код ошибки из каталога arena-portal-hr.md 8.1 (колонка
// `type`). Определяет HTTP-статус в catalog ниже.
type Kind string

const (
	KindInvalidBody           Kind = "invalid_body"
	KindUnauthenticated       Kind = "unauthenticated"
	KindForbiddenRole         Kind = "forbidden_role"
	KindForbiddenGroup        Kind = "forbidden_group"
	KindCodeBlocked           Kind = "code_blocked"
	KindNotFound              Kind = "not_found"
	KindCodeNotFound          Kind = "code_not_found"
	KindDemoDisabled          Kind = "demo_disabled"
	KindCodeRevoked           Kind = "code_revoked"
	KindAssignmentCancelled   Kind = "assignment_cancelled"
	KindAssignmentExpired     Kind = "assignment_expired"
	KindRehearsalLinkUsed     Kind = "rehearsal_link_used"
	KindRehearsalLinkExpired  Kind = "rehearsal_link_expired"
	KindSessionFinished       Kind = "session_finished"
	KindSessionRunning        Kind = "session_running"
	KindAssessmentPassed      Kind = "assessment_passed"
	KindConsentRequired       Kind = "consent_required"
	KindNoModelRoute          Kind = "no_model_route"
	KindMissingTurns          Kind = "missing_turns"
	KindModeLocked            Kind = "mode_locked"
	KindFingerprintChanged    Kind = "fingerprint_changed"
	KindNothingChanged        Kind = "nothing_changed"
	KindArchived              Kind = "archived"
	KindGenerationUnavailable Kind = "generation_unavailable"
	KindAlreadyAnswered       Kind = "already_answered"
	KindAlreadyReviewed       Kind = "already_reviewed"
	KindNotReviewable         Kind = "not_reviewable"
	KindDraftChanged          Kind = "draft_changed"
	KindTooLarge              Kind = "too_large"
	KindValidationFailed      Kind = "validation_failed"
	KindScenarioCheckFailed   Kind = "scenario_check_failed"
	KindDecisionIneligible    Kind = "decision_ineligible"
	KindRateLimited           Kind = "rate_limited"

	// KindNotImplemented и KindInternal в arena-portal-hr.md 8.1 нет — это
	// не ошибки предметной области, а состояния самого портала: операция ещё
	// не собрана (D-17) или отказ, которого каталог не предвидел. NFR-R-03
	// запрещает молчаливые отказы — любая ветка обязана ответить понятным
	// problem+json, поэтому эти два кода существуют как крайний случай.
	KindNotImplemented Kind = "not_implemented"
	KindInternal       Kind = "internal_error"
)

var statusByKind = map[Kind]int{
	KindInvalidBody:           http.StatusBadRequest,
	KindUnauthenticated:       http.StatusUnauthorized,
	KindForbiddenRole:         http.StatusForbidden,
	KindForbiddenGroup:        http.StatusForbidden,
	KindCodeBlocked:           http.StatusForbidden,
	KindNotFound:              http.StatusNotFound,
	KindCodeNotFound:          http.StatusNotFound,
	KindDemoDisabled:          http.StatusNotFound,
	KindCodeRevoked:           http.StatusGone,
	KindAssignmentCancelled:   http.StatusGone,
	KindAssignmentExpired:     http.StatusGone,
	KindRehearsalLinkUsed:     http.StatusGone,
	KindRehearsalLinkExpired:  http.StatusGone,
	KindSessionFinished:       http.StatusConflict,
	KindSessionRunning:        http.StatusConflict,
	KindAssessmentPassed:      http.StatusConflict,
	KindConsentRequired:       http.StatusConflict,
	KindNoModelRoute:          http.StatusConflict,
	KindMissingTurns:          http.StatusConflict,
	KindModeLocked:            http.StatusConflict,
	KindFingerprintChanged:    http.StatusConflict,
	KindNothingChanged:        http.StatusConflict,
	KindArchived:              http.StatusConflict,
	KindGenerationUnavailable: http.StatusServiceUnavailable,
	KindAlreadyAnswered:       http.StatusConflict,
	KindAlreadyReviewed:       http.StatusConflict,
	KindNotReviewable:         http.StatusConflict,
	KindDraftChanged:          http.StatusPreconditionFailed,
	KindTooLarge:              http.StatusRequestEntityTooLarge,
	KindValidationFailed:      http.StatusUnprocessableEntity,
	KindScenarioCheckFailed:   http.StatusUnprocessableEntity,
	KindDecisionIneligible:    http.StatusUnprocessableEntity,
	KindRateLimited:           http.StatusTooManyRequests,
	KindNotImplemented:        http.StatusNotImplemented,
	KindInternal:              http.StatusInternalServerError,
}

// Error — доменная ошибка портала. Реализует error, поэтому службы модулей
// возвращают её как обычную ошибку Go; transport.go достаёт её через
// errors.As и решает, в какой сгенерированный *ResponseObject завернуть
// Problem() (D-17: конкретный тип ответа на операцию строгий сервер знает
// только адаптер cmd/portal/api.go, поэтому саму обёртку делает transport).
type Error struct {
	Kind   Kind
	Title  string
	Detail string
	Errors []gen.FieldError
	Data   map[string]any

	// RetryAfterSeconds — для KindRateLimited: заголовок Retry-After.
	RetryAfterSeconds *int
}

func NewError(kind Kind, title string) *Error {
	return &Error{Kind: kind, Title: title}
}

func (e *Error) Error() string { return e.Title }

func (e *Error) WithDetail(detail string) *Error {
	e.Detail = detail
	return e
}

func (e *Error) WithErrors(fieldErrors []gen.FieldError) *Error {
	e.Errors = fieldErrors
	return e
}

func (e *Error) WithData(data map[string]any) *Error {
	e.Data = data
	return e
}

func (e *Error) WithRetryAfter(seconds int) *Error {
	e.RetryAfterSeconds = &seconds
	return e
}

// Status — код состояния HTTP для Kind. Неизвестный Kind (в коде такого
// быть не должно) отвечает 500, а не паникой.
func (e *Error) Status() int {
	if status, ok := statusByKind[e.Kind]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// Problem собирает тело ответа в форме, которую генератор ожидает от
// каждой операции (internal/api/gen.Problem) — тот же тип конвертируется
// в любой из *ApplicationProblemPlusJSONResponse без переразметки полей.
func (e *Error) Problem() gen.Problem {
	p := gen.Problem{
		Type:   string(e.Kind),
		Title:  e.Title,
		Status: e.Status(),
	}
	if e.Detail != "" {
		p.Detail = &e.Detail
	}
	if len(e.Errors) > 0 {
		errs := e.Errors
		p.Errors = &errs
	}
	if len(e.Data) > 0 {
		data := make(map[string]interface{}, len(e.Data))
		for k, v := range e.Data {
			data[k] = v
		}
		p.Data = &data
	}
	p.RetryAfterSeconds = e.RetryAfterSeconds
	return p
}

// NotImplemented — общая ошибка «операция ещё не собрана» (D-17): её
// возвращают все методы адаптера cmd/portal/api.go, чей модуль ещё не
// существует. 501, а не паника и не текст генератора.
func NotImplemented() *Error {
	return NewError(KindNotImplemented, "Эта операция портала ещё не реализована.")
}

// WriteError пишет problem+json по доменной ошибке. Используется вне
// сгенерированных обработчиков strict-server: серединным ПО (лимит
// размера, фильтр emotion, проверка схемы, JSON-only) и заглушкой «ещё не
// реализовано» — там нет типа ответа конкретной операции, писать можно
// только напрямую в http.ResponseWriter.
func WriteError(w http.ResponseWriter, err *Error) {
	problem := err.Problem()
	if err.RetryAfterSeconds != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*err.RetryAfterSeconds))
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(problem)
}

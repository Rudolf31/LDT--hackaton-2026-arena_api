// Package log — структурированный лог портала. Тела запросов в лог не
// попадают вообще, а имён и текстов реплик участников в нём быть не может
// ни при каких обстоятельствах (CLAUDE.md, правило 5; I-5).
package log

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// New строит slog.Logger с уровнем из ARENA_LOG_LEVEL ("debug", "info" —
// по умолчанию, "warn", "error").
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}

// forbiddenFields — то, что в лог не пишем ни под каким ключом: имена людей,
// тексты реплик и тела запросов целиком (I-5, CLAUDE.md правило 5).
var forbiddenFields = map[string]struct{}{
	"body": {}, "text": {}, "full_name": {}, "pseudonym": {},
	"message": {}, "comment": {}, "transcript": {}, "details": {},
}

// statusRecorder перехватывает код ответа: net/http его иначе не отдаёт.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// RequestMiddleware логирует метод, путь, код ответа и длительность. Тело
// запроса и ответа в лог не попадает никогда — это не забытая возможность,
// а требование (I-5, CLAUDE.md правило 5): реплика участника не должна
// уйти в лог ни при каком уровне логирования.
func RequestMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("запрос",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// SafeAttr паникует при попытке залогировать поле с запрещённым именем —
// лучше упасть в разработке, чем молча унести реплику участника в лог
// (I-5, а также NFR-R-03: никаких молчаливых отказов, здесь — по аналогии
// молчаливых нарушений).
func SafeAttr(key string, value any) slog.Attr {
	if _, forbidden := forbiddenFields[strings.ToLower(key)]; forbidden {
		panic("log.SafeAttr: поле " + key + " запрещено правилом «имён и текстов реплик в лог не пишем» (CLAUDE.md, правило 5)")
	}
	return slog.Any(key, value)
}

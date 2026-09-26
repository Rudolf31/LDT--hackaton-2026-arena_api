// Package ratelimit — скользящее окно в памяти процесса. Каждый предел из
// arena-portal-hr.md 8.1 — отдельный *Limiter со своим ключом (адрес,
// токен, пользователь или пара «адрес + логин»); при перезапуске счётчики
// теряются, это принятое поведение (arena-portal-backend-architecture.md 3.2).
package ratelimit

import (
	"sync"
	"time"
)

// Limiter — скользящее окно на N попыток за window по произвольному ключу.
type Limiter struct {
	limit  int
	window time.Duration

	mu   sync.Mutex
	hits map[string][]time.Time
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, hits: make(map[string][]time.Time)}
}

// Allow сообщает, разрешена ли ещё одна попытка по ключу прямо сейчас, и
// сама её засчитывает, если да. При отказе retryAfter — через сколько
// секунд имеет смысл повторить (arena-portal-hr.md 8.1: «Retry-After в
// секундах»).
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	hits := l.hits[key]
	kept := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= l.limit {
		retryAfter = kept[0].Add(l.window).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		l.hits[key] = kept
		return false, retryAfter
	}

	kept = append(kept, now)
	l.hits[key] = kept
	return true, 0
}

// Пределы частоты запросов — arena-portal-hr.md 8.1, таблица «Частота
// запросов». Используются модулями, которые вызывают соответствующие
// адреса (auth, sessions, generation, rehearsals); сюда вынесены как
// константы, чтобы предел не разъезжался по модулям в самостоятельно
// придуманных числах.
const (
	// Ввод кода доступа: 10 в минуту с адреса, постоянная блокировка кода —
	// отдельно, по числу неудач из portal_settings.
	CodeAttemptLimit  = 10
	CodeAttemptWindow = time.Minute

	// Выдача настроек тренажёра, старт сессии, повторная выдача, погашение
	// ссылки репетиции: 30 в минуту на токен.
	TrainerTokenLimit  = 30
	TrainerTokenWindow = time.Minute

	// Демо-вход и погашение ссылки репетиции гостем — отдельно, 10 в минуту
	// с адреса (у гостя нет токена, пока он не вошёл).
	DemoEnterLimit  = 10
	DemoEnterWindow = time.Minute

	// Генерация и правка сценария моделью (все четыре адреса авторства,
	// D-05): 10 в час на пользователя портала.
	GenerationLimit  = 10
	GenerationWindow = time.Hour

	// Вход в портал: 5 в минуту на пару «адрес + логин».
	PortalLoginLimit  = 5
	PortalLoginWindow = time.Minute
)

// Limiters собирает все пять счётчиков в одну точку сборки для
// cmd/portal/main.go (CLAUDE.md: «зависимости собираются явно в
// cmd/portal/main.go»).
type Limiters struct {
	CodeAttempt  *Limiter
	TrainerToken *Limiter
	DemoEnter    *Limiter
	Generation   *Limiter
	PortalLogin  *Limiter
}

func NewLimiters() *Limiters {
	return &Limiters{
		CodeAttempt:  New(CodeAttemptLimit, CodeAttemptWindow),
		TrainerToken: New(TrainerTokenLimit, TrainerTokenWindow),
		DemoEnter:    New(DemoEnterLimit, DemoEnterWindow),
		Generation:   New(GenerationLimit, GenerationWindow),
		PortalLogin:  New(PortalLoginLimit, PortalLoginWindow),
	}
}

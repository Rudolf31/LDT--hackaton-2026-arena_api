// Package actor — кто выполняет запрос портала. Кладёт его в контекст
// middleware модуля auth, читают transport.go всех модулей: импортировать
// сам auth им запрещает линтер границ (D-18), а знать пользователя нужно.
package actor

import (
	"context"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
)

// Actor — пользователь портала, уже прочитанный из базы на этом запросе
// (роль из cookie не берётся — CLAUDE.md, правило 3).
type Actor struct {
	UserID uuid.UUID
	Role   gen.Role
}

func (a Actor) IsAdmin() bool { return a.Role == gen.Admin }

type ctxKey struct{}

func With(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

func From(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(Actor)
	return a, ok
}

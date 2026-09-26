// Package pg — пул соединений с базой, помощник транзакции и разбор кодов
// ошибок PostgreSQL в доменные (arena-portal-backend-architecture.md 3.2, 6.3).
package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool открывает пул к базе под ролью arena_app. Роль без прав DDL —
// так и должно быть (CLAUDE.md, правило 7).
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("подключение к базе: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("база не отвечает: %w", err)
	}
	return pool, nil
}

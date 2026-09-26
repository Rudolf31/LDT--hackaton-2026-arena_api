package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTx открывает одну транзакцию на один сценарий использования (CLAUDE.md,
// «Устройство модуля»): fn выполняет и правило, и запись в журнал одной и той
// же транзакцией. Паника внутри fn откатывает транзакцию и улетает дальше.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("открытие транзакции: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()

	err = fn(ctx, tx)
	return err
}

// WithSavepoint выполняет fn внутри точки сохранения name: при ошибке —
// откат до неё, транзакция снаружи продолжает жить (arena-portal-backend-architecture.md
// 6.3, выпуск кодов доступа). Вызывающий код решает, повторять ли попытку.
func WithSavepoint(ctx context.Context, tx pgx.Tx, name string, fn func(ctx context.Context) error) error {
	if _, err := tx.Exec(ctx, "SAVEPOINT "+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("точка сохранения %s: %w", name, err)
	}

	if err := fn(ctx); err != nil {
		if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+pgx.Identifier{name}.Sanitize()); rbErr != nil {
			return errors.Join(err, fmt.Errorf("откат до точки сохранения %s: %w", name, rbErr))
		}
		return err
	}

	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("освобождение точки сохранения %s: %w", name, err)
	}
	return nil
}

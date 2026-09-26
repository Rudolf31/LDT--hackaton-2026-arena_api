//go:build integration

package schema

import (
	"context"
	"testing"

	"arena-portal-backend/internal/platform/pgtest"
)

// Готово, когда (00-bootstrap.md): «интеграционный тест подключается под
// arena_app и видит 20 таблиц».
func TestArenaAppSeesAllTables(t *testing.T) {
	pool := pgtest.NewDatabase(t)

	var count int
	err := pool.QueryRow(context.Background(),
		`select count(*) from information_schema.tables where table_schema = 'arena' and table_type = 'BASE TABLE'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("запрос information_schema.tables: %v", err)
	}
	if count != 20 {
		t.Fatalf("arena_app видит %d таблиц в схеме arena, ожидали 20", count)
	}
}

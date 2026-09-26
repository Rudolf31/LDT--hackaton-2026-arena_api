//go:build integration

package schema

import (
	"context"
	"testing"

	"arena-portal-backend/internal/platform/pgtest"
)

// NFR-PR-03 (arena-portal-hr.md 13.5): «тест сверяет список столбцов схемы
// arena из information_schema.columns с таблицей „поле → категория →
// основание → срок → кто удаляет“, поэтому новый столбец без строки в ней
// роняет сборку». Таблица (Retention в retention.go) заполняется по мере
// реализации модулей, полностью — до этапа 11; до тех пор этот тест ожидаемо
// падает на незаполненных столбцах, поэтому идёт под тегом integration и не
// входит в обычный `go test ./...`.
func TestRetentionMatchesSchema(t *testing.T) {
	pool := pgtest.NewDatabase(t)

	rows, err := pool.Query(context.Background(),
		`select table_name, column_name from information_schema.columns where table_schema = 'arena' order by table_name, column_name`,
	)
	if err != nil {
		t.Fatalf("запрос information_schema.columns: %v", err)
	}
	defer rows.Close()

	have := map[[2]string]bool{}
	for _, f := range Retention {
		have[[2]string{f.Table, f.Column}] = true
	}

	var missing []string
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("чтение information_schema.columns: %v", err)
		}
		if !have[[2]string{table, column}] {
			missing = append(missing, table+"."+column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("information_schema.columns: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("NFR-PR-03: %d столбцов без строки в таблице retention.go: %v", len(missing), missing)
	}
}

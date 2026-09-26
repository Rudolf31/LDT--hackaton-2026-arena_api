package schema

import "testing"

func TestRetentionNoDuplicates(t *testing.T) {
	seen := map[[2]string]bool{}
	for _, f := range Retention {
		key := [2]string{f.Table, f.Column}
		if seen[key] {
			t.Fatalf("повтор строки NFR-PR-03 для %s.%s", f.Table, f.Column)
		}
		seen[key] = true
		if f.Category == "" || f.Basis == "" || f.Retention == "" || f.DeletedBy == "" {
			t.Fatalf("строка NFR-PR-03 для %s.%s неполная: все поля обязательны", f.Table, f.Column)
		}
	}
}

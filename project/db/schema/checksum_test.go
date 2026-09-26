// Package schema хранит неизменяемую копию схемы базы данных.
package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// Контрольная сумма arena-db.sql на момент копирования в 00-bootstrap.
// Схему создаёт роль-владелец; приложение (и мы) её не правит — дальнейшие
// изменения идут нумерованными файлами поверх (CLAUDE.md, правило 7; I-7).
const wantSHA256 = "d9ed2e45f7497f89b9cb080d3a3d2e6d34271e8eb6367922773aedb88e649f94"

func TestArenaDBSQLUnchanged(t *testing.T) {
	data, err := os.ReadFile("arena-db.sql")
	if err != nil {
		t.Fatalf("чтение arena-db.sql: %v", err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != wantSHA256 {
		t.Fatalf("arena-db.sql изменился: sha256 = %s, ожидали %s — этот файл не правим, изменения схемы — только нумерованными файлами поверх", got, wantSHA256)
	}
}

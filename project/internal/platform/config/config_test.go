package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ARENA_DATABASE_URL", "postgres://localhost/arena")
	t.Setenv("ARENA_MASTER_KEY", validKey())
	t.Setenv("ARENA_CODE_HMAC_SECRET", validKey())
}

func TestLoadMissingMasterKey(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ARENA_MASTER_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка без ARENA_MASTER_KEY")
	}
	if !strings.Contains(err.Error(), "ARENA_MASTER_KEY") {
		t.Fatalf("ошибка не называет переменную: %v", err)
	}
}

func TestLoadMasterKeyNotBase64(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ARENA_MASTER_KEY", "это не base64!!!")

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка на невалидном base64")
	}
	if !strings.Contains(err.Error(), "ARENA_MASTER_KEY") {
		t.Fatalf("ошибка не называет переменную: %v", err)
	}
}

func TestLoadMasterKeyWrongLength(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ARENA_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка на ключе длиной не 32 байта")
	}
	if !strings.Contains(err.Error(), "ARENA_MASTER_KEY") || !strings.Contains(err.Error(), "16") {
		t.Fatalf("ошибка не называет переменную и длину: %v", err)
	}
}

func TestLoadOK(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(cfg.MasterKey) != 32 || len(cfg.CodeHMACSecret) != 32 {
		t.Fatalf("ключи должны быть по 32 байта: master=%d code=%d", len(cfg.MasterKey), len(cfg.CodeHMACSecret))
	}
	if !cfg.Demo {
		t.Fatal("ARENA_DEMO по умолчанию должен быть включён")
	}
	if cfg.StartedAt.IsZero() {
		t.Fatal("StartedAt должен быть заполнен")
	}
}

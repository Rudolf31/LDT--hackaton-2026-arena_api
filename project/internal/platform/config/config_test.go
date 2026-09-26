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
	if cfg.GenConfigured() {
		t.Fatal("без ARENA_GEN_KEY/ARENA_GEN_MODEL генерация не должна считаться настроенной")
	}
	if cfg.STTConfigured() {
		t.Fatal("без ARENA_STT_MODEL расшифровка не должна считаться настроенной")
	}
	if cfg.GenURL != defaultGenURL {
		t.Fatalf("ARENA_GEN_URL по умолчанию должен быть облаком OpenRouter, получено %q", cfg.GenURL)
	}
}

// TestLoadGenerationDefaults — D-35: ARENA_STT_KEY/ARENA_STT_URL по
// умолчанию берутся из ARENA_GEN_KEY/ARENA_GEN_URL, а ARENA_GEN_KEY и
// ARENA_GEN_MODEL включают генерацию, ARENA_STT_MODEL — расшифровку.
func TestLoadGenerationDefaults(t *testing.T) {
	setValidEnv(t)
	t.Setenv("ARENA_GEN_KEY", "gen-key")
	t.Setenv("ARENA_GEN_MODEL", "gen-model")
	t.Setenv("ARENA_STT_MODEL", "stt-model")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !cfg.GenConfigured() {
		t.Fatal("с ARENA_GEN_KEY и ARENA_GEN_MODEL генерация должна считаться настроенной")
	}
	if !cfg.STTConfigured() {
		t.Fatal("с ARENA_STT_MODEL расшифровка должна считаться настроенной")
	}
	if cfg.STTKey != "gen-key" {
		t.Fatalf("ARENA_STT_KEY по умолчанию должен быть равен ARENA_GEN_KEY, получено %q", cfg.STTKey)
	}
	if cfg.STTURL != defaultGenURL {
		t.Fatalf("ARENA_STT_URL по умолчанию должен быть равен ARENA_GEN_URL, получено %q", cfg.STTURL)
	}

	t.Setenv("ARENA_STT_KEY", "stt-key")
	t.Setenv("ARENA_STT_URL", "https://stt.example/v1")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if cfg.STTKey != "stt-key" || cfg.STTURL != "https://stt.example/v1" {
		t.Fatalf("явно заданные ARENA_STT_KEY/ARENA_STT_URL не должны подменяться значениями по умолчанию: %+v", cfg)
	}
}

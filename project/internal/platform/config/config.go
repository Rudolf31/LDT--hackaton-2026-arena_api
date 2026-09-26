// Package config читает и проверяет переменные окружения портала
// (arena-portal-backend-architecture.md 11.2). Ни одна из них не подставляется
// по умолчанию, кроме тех, что документ прямо помечает необязательными:
// молча стартовать без обязательной переменной нельзя (CLAUDE.md, правило 8).
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const keyLengthBytes = 32

type Config struct {
	DatabaseURL string
	// MasterKey и CodeHMACSecret — уже декодированные из base64, ровно 32 байта
	// (D-15). В базу и в лог не попадают.
	MasterKey      []byte
	CodeHMACSecret []byte
	Listen         string

	STTURL, STTKey, STTModel string
	GenURL, GenKey, GenModel string

	Demo          bool
	DemoModelKeys bool
	LogLevel      string

	// CookieSecure — флаг Secure у cookie arena_session (D-22). Выключается
	// только на стенде без TLS: по обычному http браузер такую cookie не
	// вернёт, и вход молча не сработает.
	CookieSecure bool

	// StartedAt — момент запуска процесса. Используется job'ом закрытия
	// брошенных сессий (arena-portal-backend-architecture.md 7.3): время
	// недоступности портала не входит в таймаут (FR-ST-03).
	StartedAt time.Time
}

// Load читает конфигурацию из окружения. Возвращённая ошибка уже содержит
// готовую русскую фразу — её можно печатать в лог и в problem+json как есть.
func Load() (Config, error) {
	startedAt := time.Now()

	var missing []string
	required := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}

	databaseURL := required("ARENA_DATABASE_URL")
	rawMasterKey := required("ARENA_MASTER_KEY")
	rawCodeHMACSecret := required("ARENA_CODE_HMAC_SECRET")

	if len(missing) > 0 {
		return Config{}, fmt.Errorf(
			"не заданы обязательные переменные окружения: %s — портал не может стартовать без них; "+
				"ARENA_MASTER_KEY и ARENA_CODE_HMAC_SECRET задаются командой `openssl rand -base64 32`",
			strings.Join(missing, ", "),
		)
	}

	masterKey, err := decodeKey("ARENA_MASTER_KEY", rawMasterKey)
	if err != nil {
		return Config{}, err
	}
	codeHMACSecret, err := decodeKey("ARENA_CODE_HMAC_SECRET", rawCodeHMACSecret)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		DatabaseURL:    databaseURL,
		MasterKey:      masterKey,
		CodeHMACSecret: codeHMACSecret,
		Listen:         orDefault("ARENA_LISTEN", ":8080"),

		STTURL:   os.Getenv("ARENA_STT_URL"),
		STTKey:   os.Getenv("ARENA_STT_KEY"),
		STTModel: os.Getenv("ARENA_STT_MODEL"),
		GenURL:   os.Getenv("ARENA_GEN_URL"),
		GenKey:   os.Getenv("ARENA_GEN_KEY"),
		GenModel: os.Getenv("ARENA_GEN_MODEL"),

		Demo:          orDefaultBool("ARENA_DEMO", true),
		DemoModelKeys: orDefaultBool("DEMO_MODEL_KEYS", false),
		LogLevel:      orDefault("ARENA_LOG_LEVEL", "info"),
		CookieSecure:  orDefaultBool("ARENA_COOKIE_SECURE", true),

		StartedAt: startedAt,
	}

	return cfg, nil
}

// decodeKey декодирует секрет из base64 и проверяет длину (D-15). Ошибка
// называет переменную и что именно с ней не так — без этого отказ старта
// бесполезен для того, кто его увидит в логе.
func decodeKey(envName, raw string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf(
			"переменная %s не в base64 (%v) — сгенерируйте её командой `openssl rand -base64 32`",
			envName, err,
		)
	}
	if len(decoded) != keyLengthBytes {
		return nil, fmt.Errorf(
			"переменная %s после декодирования из base64 даёт %d байт, а нужно ровно %d — "+
				"сгенерируйте её командой `openssl rand -base64 32`",
			envName, len(decoded), keyLengthBytes,
		)
	}
	return decoded, nil
}

// GenerationConfigured сообщает, настроены ли обе модели авторства сценария
// (расшифровка речи и генерация документа). Без них адреса авторства отвечают
// 503 с русским текстом, а форма и остальной портал работают как обычно.
func (c Config) GenerationConfigured() bool {
	return c.STTURL != "" && c.STTKey != "" && c.GenURL != "" && c.GenKey != ""
}

// Describe — строка режима для лога при старте (архитектура 11.2): без
// секретов, только то, что влияет на поведение.
func (c Config) Describe() string {
	return fmt.Sprintf(
		"демо-режим=%v, ключи демо-гостям=%v, авторство сценария настроено=%v, cookie Secure=%v, адрес=%s",
		c.Demo, c.DemoModelKeys, c.GenerationConfigured(), c.CookieSecure, c.Listen,
	)
}

func orDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func orDefaultBool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

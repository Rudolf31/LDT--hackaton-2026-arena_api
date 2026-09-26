//go:build integration

// Package pgtest — «чистая база на тест»: одноразовая база для интеграционных
// тестов. NewDatabase создаёт пустую базу, накатывает db/schema/arena-db.sql
// ролью-владельцем (та же побайтная копия, что проверяет TestArenaDBSQLUnchanged)
// и отдаёт тесту пул под arena_app — без прав DDL, как в проде (CLAUDE.md,
// правило 7). Нужен поднятый Postgres (`make up`); `make test-integration`
// подхватывает .env, и адрес роли-владельца собирается из POSTGRES_SUPERUSER
// и POSTGRES_SUPERUSER_PASSWORD, если ARENA_TEST_ADMIN_DSN не задан явно.
package pgtest

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultAdminDSN    = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	defaultAppPassword = "arena_app_test_password"

	// schemaLockKey — ключ рекомендательной блокировки, под которой тесты
	// накатывают схему и задают пароль arena_app. Роль arena_app — общая на
	// весь кластер: без блокировки тесты из разных пакетов (go test гоняет
	// пакеты параллельно) одновременно меняют одну строку pg_authid, и
	// Postgres отвечает «tuple concurrently updated».
	schemaLockKey = 0x61726e61 // "arna"
)

// NewDatabase создаёт одноразовую базу, накатывает схему ролью-владельцем
// и возвращает пул под arena_app. t.Cleanup закрывает пул и дропает базу.
func NewDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	adminDSN := adminDSNFromEnv()
	// Пароль arena_app по умолчанию — тот же, что у запущенного портала
	// (ARENA_APP_PASSWORD из .env): тесты работают в том же кластере, и
	// другой пароль отрезал бы портал от базы при следующем подключении.
	appPassword := envOr("ARENA_TEST_APP_PASSWORD", envOr("ARENA_APP_PASSWORD", defaultAppPassword))

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("подключение ролью-владельцем (%s): %v — нужен поднятый Postgres (`make up`), пароль владельца — POSTGRES_SUPERUSER_PASSWORD в .env", redacted(adminDSN), err)
	}
	defer admin.Close(ctx)

	dbName := fmt.Sprintf("arena_test_%d_%d", time.Now().UnixNano(), rand.Intn(1_000_000))
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{dbName}.Sanitize()); err != nil {
		t.Fatalf("создание тестовой базы %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		c, err := pgx.Connect(cleanupCtx, adminDSN)
		if err != nil {
			return
		}
		defer c.Close(cleanupCtx)
		_, _ = c.Exec(cleanupCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{dbName}.Sanitize()+` WITH (FORCE)`)
	})

	dbDSN := withDatabase(adminDSN, dbName)
	dbOwner, err := pgx.Connect(ctx, dbDSN)
	if err != nil {
		t.Fatalf("подключение ролью-владельцем к %s: %v", dbName, err)
	}
	defer dbOwner.Close(ctx)

	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock($1)`, schemaLockKey); err != nil {
		t.Fatalf("блокировка накатки схемы: %v", err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, `SELECT pg_advisory_unlock($1)`, schemaLockKey); err != nil {
			t.Errorf("снятие блокировки накатки схемы: %v", err)
		}
	}()

	schemaSQL, err := os.ReadFile(schemaPath())
	if err != nil {
		t.Fatalf("чтение db/schema/arena-db.sql: %v", err)
	}
	if _, err := dbOwner.Exec(ctx, string(schemaSQL)); err != nil {
		t.Fatalf("накатка схемы в %s: %v", dbName, err)
	}
	if _, err := dbOwner.Exec(ctx, fmt.Sprintf(`ALTER ROLE arena_app PASSWORD '%s'`, appPassword)); err != nil {
		t.Fatalf("пароль arena_app: %v", err)
	}

	appDSN := withUser(dbDSN, "arena_app", appPassword)
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("пул под arena_app: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("arena_app не может подключиться к %s: %v", dbName, err)
	}
	return pool
}

func schemaPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "db", "schema", "arena-db.sql")
}

func withDatabase(dsn, dbName string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(fmt.Sprintf("неверный DSN %q: %v", dsn, err))
	}
	u.Path = "/" + dbName
	return u.String()
}

func withUser(dsn, user, password string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(fmt.Sprintf("неверный DSN %q: %v", dsn, err))
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

// adminDSNFromEnv — адрес роли-владельца: ARENA_TEST_ADMIN_DSN, иначе
// собранный из POSTGRES_SUPERUSER/POSTGRES_SUPERUSER_PASSWORD (.env для
// docker compose), иначе значение по умолчанию. Пароль кодируется в URL
// библиотекой — спецсимволы в нём адрес не ломают.
func adminDSNFromEnv() string {
	if v := os.Getenv("ARENA_TEST_ADMIN_DSN"); v != "" {
		return v
	}
	password := os.Getenv("POSTGRES_SUPERUSER_PASSWORD")
	if password == "" {
		return defaultAdminDSN
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(envOr("POSTGRES_SUPERUSER", "postgres"), password),
		Host:     "localhost:5432",
		Path:     "/postgres",
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// redacted прячет пароль в адресе для сообщения об ошибке: теперь это
// настоящий пароль из .env, а не тестовое значение по умолчанию.
func redacted(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "(адрес не разбирается)"
	}
	return u.Redacted()
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

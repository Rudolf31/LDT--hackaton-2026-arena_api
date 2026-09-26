//go:build integration

// Package pgtest — «чистая база на тест»: одноразовая база для интеграционных
// тестов. NewDatabase создаёт пустую базу, накатывает db/schema/arena-db.sql
// ролью-владельцем (та же побайтная копия, что проверяет TestArenaDBSQLUnchanged)
// и отдаёт тесту пул под arena_app — без прав DDL, как в проде (CLAUDE.md,
// правило 7). Нужен поднятый Postgres: `docker compose -f deploy/docker-compose.yml
// up -d postgres`, адрес — в ARENA_TEST_ADMIN_DSN.
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
)

// NewDatabase создаёт одноразовую базу, накатывает схему ролью-владельцем
// и возвращает пул под arena_app. t.Cleanup закрывает пул и дропает базу.
func NewDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	adminDSN := envOr("ARENA_TEST_ADMIN_DSN", defaultAdminDSN)
	appPassword := envOr("ARENA_TEST_APP_PASSWORD", defaultAppPassword)

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("подключение ролью-владельцем (%s): %v — нужен поднятый Postgres, см. deploy/docker-compose.yml", adminDSN, err)
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

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

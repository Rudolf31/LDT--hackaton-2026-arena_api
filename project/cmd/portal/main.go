// Command portal — единственный процесс бэкенда: модульный монолит поверх
// PostgreSQL. Сборка — явная, здесь и только здесь (CLAUDE.md, «Зависимости
// собираются явно в cmd/portal/main.go, без контейнера зависимостей»);
// каждый следующий этап добавляет сюда свой модуль.
package main

import (
	"context"
	"fmt"
	stdlog "log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	arenaapi "arena-portal-backend/api"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/demo"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/modules/settings"
	"arena-portal-backend/internal/platform/config"
	"arena-portal-backend/internal/platform/httpx"
	arenalog "arena-portal-backend/internal/platform/log"
	"arena-portal-backend/internal/platform/pg"
	"arena-portal-backend/internal/platform/ratelimit"
)

// maxBodyBytesDefault и исключения — arena-portal-hr.md 8.1: 1 МБ на тело,
// 5 МБ на запись репетиции, 25 МБ на mp3 (два адреса авторства, D-05, вне
// этого механизма — у них нет схемы в контракте и своя проверка тела).
const maxBodyBytesDefault = 1 << 20 // 1 МиБ

var maxBodyBytesByOperationID = map[string]int64{
	"trainerUploadRehearsal": 5 << 20, // 5 МиБ
}

func main() {
	if err := run(); err != nil {
		stdlog.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := arenalog.New(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pg.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	handler, err := buildApp(ctx, pool, cfg, logger)
	if err != nil {
		return err
	}

	logger.Info("арена переговоров: портал стартует", "режим", cfg.Describe())

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		logger.Info("получен сигнал остановки, завершаю обслуживание запросов")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}

// buildApp собирает модули и роутер и делает первичную заливку. Вынесена
// из run, чтобы интеграционные тесты собирали портал тем же кодом.
func buildApp(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) (http.Handler, error) {
	bodySchemas, err := httpx.LoadBodySchemas(arenaapi.Spec, maxBodyBytesDefault, maxBodyBytesByOperationID)
	if err != nil {
		return nil, fmt.Errorf("загрузка схем тел из контракта: %w", err)
	}
	operationAccess, err := httpx.LoadOperationAccess(arenaapi.Spec)
	if err != nil {
		return nil, fmt.Errorf("загрузка ролей операций из контракта: %w", err)
	}
	limiters := ratelimit.NewLimiters()

	// --- модули (порядок — по зависимостям, CLAUDE.md, «Устройство модуля») ---
	auditModule := audit.New()
	authModule, err := auth.New(pool, auditModule, auth.Config{
		HMACSecret: cfg.CodeHMACSecret, CookieSecure: cfg.CookieSecure, Demo: cfg.Demo,
	}, limiters.PortalLogin, operationAccess)
	if err != nil {
		return nil, err
	}
	peopleModule := people.New(pool, auditModule, authModule.GroupAccess(), noAssignmentsYet{}, cfg.MasterKey)
	settingsModule := settings.New(pool, auditModule, cfg.Demo)
	scenariosModule := scenarios.New(pool, auditModule, settingsModule.Service(), noRehearsalsYet{}, noSessionsYet{})
	demoModule := demo.New(pool, authModule.Provisioner(), peopleModule.Provisioner())

	seeded, err := demoModule.SeedIfEmpty(ctx)
	if err != nil {
		return nil, fmt.Errorf("первичная заливка данных: %w", err)
	}
	if len(seeded) > 0 {
		logger.Warn("база была пустой: заведены демо-пользователи с паролями из README — смените их пароли",
			"логины", seeded)
	}

	a := &api{
		auth:      authModule.Transport,
		people:    peopleModule.Transport,
		audit:     auditModule.NewTransport(pool, authModule.Directory(), peopleModule.Directory()),
		settings:  settingsModule,
		scenarios: scenariosModule.Transport,
	}
	return buildRouter(a, authModule.Transport.Middleware, bodySchemas, logger), nil
}

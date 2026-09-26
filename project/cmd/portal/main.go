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
	"arena-portal-backend/internal/modules/assignments"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/demo"
	"arena-portal-backend/internal/modules/generation"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/modules/sessions"
	"arena-portal-backend/internal/modules/settings"
	"arena-portal-backend/internal/platform/ai"
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
	// Зависимости по кругу связываются пересылками из late.go (этап 07).
	cancellerRef, audienceRef := &lateCanceller{}, &lateAudience{}
	factsRef, versionCountsRef, runningRef := &lateSessionFacts{}, &lateVersionCounts{}, &lateRunning{}

	auditModule := audit.New()
	authModule, err := auth.New(pool, auditModule, auth.Config{
		HMACSecret: cfg.CodeHMACSecret, CookieSecure: cfg.CookieSecure, Demo: cfg.Demo,
	}, auth.Limiters{
		PortalLogin: limiters.PortalLogin, CodeAttempt: limiters.CodeAttempt, TrainerToken: limiters.TrainerToken,
	}, operationAccess)
	if err != nil {
		return nil, err
	}
	peopleModule := people.New(pool, auditModule, authModule.GroupAccess(), cancellerRef, cfg.MasterKey)
	settingsModule := settings.New(pool, auditModule, cfg.Demo)
	scenariosModule := scenarios.New(pool, auditModule, settingsModule.Service(), noRehearsalsYet{}, versionCountsRef)
	// Профили и согласия (этап 06). «Проверить профиль» — заглушка «не
	// проверялось» (архитектура 10.3).
	profilesModule := profiles.New(pool, auditModule, cfg.MasterKey, nil, runningRef)
	consentsModule := consents.New(pool, auditModule, authModule.GroupAccess(), peopleModule.Service(),
		profilesModule.Service(), audienceRef)
	// Назначения и вход в тренажёр (этап 07).
	assignmentsModule := assignments.New(pool, assignments.Deps{
		Audit: auditModule, Access: authModule.GroupAccess(), People: peopleModule.Service(),
		Profiles: profilesModule.Service(), Versions: scenariosModule.Versions(), Settings: settingsModule.Service(),
		Sessions: factsRef, CodeSecret: cfg.CodeHMACSecret, TrainerURL: cfg.TrainerURL,
	})
	sessionsModule := sessions.New(pool, sessions.Deps{
		Entry: assignmentsModule.Entry(), Versions: scenariosModule.Versions(), People: peopleModule.Service(),
		Profiles: profilesModule.Service(), Consents: consentsModule.Service(), Tokens: authModule.TrainerTokens(),
	})
	cancellerRef.target = assignmentsModule.Canceller()
	audienceRef.target = sessionsModule.Audience()
	factsRef.target = sessionsModule.Facts()
	versionCountsRef.target = sessionsModule.VersionCounts()
	runningRef.target = sessionsModule.Running()
	demoModule := demo.New(pool, authModule.Provisioner(), peopleModule.Provisioner(),
		profilesModule.Provisioner(), consentsModule.Provisioner())

	// Авторство сценария голосом и текстом (этап 05, D-34/D-35): клиенты
	// моделей собираются только когда настроены — иначе nil, и generation
	// сам отвечает 503 на адресах, которым нужна отсутствующая модель.
	// baseCtx заданий — ctx процесса (тот же, что слушает сигнал остановки
	// в run()), не контекст HTTP-запроса, который задание запустил.
	var generator ai.ScenarioGenerator
	if cfg.GenConfigured() {
		prompt, err := generation.BuildSystemPrompt()
		if err != nil {
			return nil, fmt.Errorf("сборка системного промпта генерации: %w", err)
		}
		generator = ai.NewGenerator(cfg.GenURL, cfg.GenKey, cfg.GenModel, prompt)
	}
	var transcriber ai.Transcriber
	if cfg.STTConfigured() {
		transcriber = ai.NewTranscriber(cfg.STTURL, cfg.STTKey, cfg.STTModel)
	}
	generationModule := generation.New(scenariosModule.Authoring(), transcriber, generator, limiters.Generation, logger, ctx)
	if err := generationModule.Recover(ctx); err != nil {
		return nil, fmt.Errorf("закрытие зависших заданий авторства при старте: %w", err)
	}

	baseline, err := demoModule.EnsureBaseline(ctx)
	if err != nil {
		return nil, fmt.Errorf("базовая заливка (профиль по умолчанию, тексты согласий): %w", err)
	}
	if baseline.DefaultProfileCreated {
		logger.Warn("заведён профиль тренажёра по умолчанию без ключей — задайте ключ OpenRouter в портале")
	}
	if baseline.ConsentTextsCreated > 0 {
		logger.Info("заведены тексты согласий версии 1 с демо-реквизитами оператора", "видов", baseline.ConsentTextsCreated)
	}

	seeded, err := demoModule.SeedIfEmpty(ctx)
	if err != nil {
		return nil, fmt.Errorf("первичная заливка данных: %w", err)
	}
	if len(seeded) > 0 {
		logger.Warn("база была пустой: заведены демо-пользователи с паролями из README — смените их пароли",
			"логины", seeded)
	}

	a := &api{
		auth:        authModule.Transport,
		people:      peopleModule.Transport,
		audit:       auditModule.NewTransport(pool, authModule.Directory(), peopleModule.Directory()),
		settings:    settingsModule,
		scenarios:   scenariosModule.Transport,
		generation:  generationModule.Transport,
		profiles:    profilesModule.Transport,
		consents:    consentsModule.Transport,
		assignments: assignmentsModule.Transport,
		sessions:    sessionsModule.Transport,
	}
	return buildRouter(a, authModule.Transport.Middleware, authModule.Transport.PortalMiddleware, bodySchemas, logger), nil
}

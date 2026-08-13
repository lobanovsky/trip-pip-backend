package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// В Alpine нет /usr/share/zoneinfo. Напоминаниям об истечении сроков
	// документов нужен time.LoadLocation для Europe/Moscow (или того часового
	// пояса, что указан в APP_TIMEZONE), поэтому база часовых поясов
	// встраивается прямо в бинарник.
	_ "time/tzdata"

	"github.com/lobanovsky/trip-pip-backend/internal/auth"
	"github.com/lobanovsky/trip-pip-backend/internal/buildinfo"
	"github.com/lobanovsky/trip-pip-backend/internal/config"
	"github.com/lobanovsky/trip-pip-backend/internal/httpapi"
	"github.com/lobanovsky/trip-pip-backend/internal/pg"
	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Логгер ещё не создан: уровень логирования определяется конфигом,
		// а о поломанном конфиге всё равно нужно куда-то сообщить.
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := httpapi.Deps{
		Location:      cfg.Location,
		SessionTTL:    cfg.SessionTTL,
		SecureCookies: cfg.SecureCookies,
	}

	// DatabaseURL может быть пустым: тогда сервис обслуживает только
	// /api/ping и /api/version. Это позволяет healthcheck контейнера и
	// проверке деплоя проходить, даже пока база данных ещё не развёрнута.
	if cfg.HasDatabase() {
		pool, err := pg.NewPool(ctx, pg.PoolConfig{
			URL:            cfg.DatabaseURL,
			MaxConns:       cfg.DBMaxConns,
			ConnectTimeout: cfg.DBConnectTimeout,
		})
		if err != nil {
			logger.Error("database unavailable", "error", err)
			os.Exit(1)
		}
		defer pool.Close()

		if cfg.RunMigrations {
			if err := pg.Migrate(ctx, pool, logger); err != nil {
				logger.Error("migrations failed", "error", err)
				os.Exit(1)
			}
		}

		dataStore := store.New(pool)
		deps.Store = dataStore

		if cfg.Bootstrap.Enabled() {
			if err := runBootstrap(ctx, dataStore, cfg.Bootstrap, cfg.Location); err != nil {
				logger.Error("bootstrap failed", "error", err)
				os.Exit(1)
			}
		}

		go runSessionJanitor(ctx, dataStore, logger)
	} else {
		logger.Warn("starting without a database: DATABASE_URL is empty, domain routes will answer 503")
	}

	addr := cfg.HTTPAddr

	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(logger, buildinfo.Commit, deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server started", "address", addr, "version", buildinfo.Commit)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("HTTP server stopped")
}

// runBootstrap создаёт первое агентство и учётную запись администратора.
// Публичного эндпоинта регистрации нет, поэтому это единственный способ
// завести самую первую учётную запись; функция идемпотентна и ничего не
// делает, если в базе уже есть хотя бы один пользователь.
func runBootstrap(ctx context.Context, dataStore *store.Store, cfg config.Bootstrap, location *time.Location) error {
	hash, err := auth.HashPassword(cfg.Password)
	if err != nil {
		return err
	}

	timezone := "Europe/Moscow"
	if location != nil {
		timezone = location.String()
	}

	_, err = dataStore.EnsureBootstrap(ctx, store.BootstrapRequest{
		AgencyName:   cfg.AgencyName,
		Timezone:     timezone,
		Email:        cfg.Email,
		FullName:     "Администратор",
		PasswordHash: hash,
	})

	return err
}

// runSessionJanitor периодически удаляет сессии, которые уже настолько
// старые, что не могут никого аутентифицировать. Это не часть обработки
// запроса: медленная уборка не должна задерживать вход в систему.
func runSessionJanitor(ctx context.Context, dataStore *store.Store, logger *slog.Logger) {
	const interval = 6 * time.Hour

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sweep := func() {
		removed, err := dataStore.DeleteExpiredSessions(ctx)
		if err != nil {
			logger.Error("session cleanup failed", "error", err)

			return
		}
		if removed > 0 {
			logger.Info("expired sessions removed", "count", removed)
		}
	}

	sweep()

	for {
		select {
		case <-ticker.C:
			sweep()
		case <-ctx.Done():
			return
		}
	}
}

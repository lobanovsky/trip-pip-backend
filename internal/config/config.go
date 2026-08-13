// Package config читает конфигурацию процесса из переменных окружения.
//
// Каждое значение разбирается и проверяется один раз при старте, поэтому
// опечатка сразу же роняет процесс, а не всплывает непонятной ошибкой при
// первом запросе, которому это значение понадобилось.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr         = ":8080"
	defaultDBMaxConns       = 10
	defaultDBConnectTimeout = 10 * time.Second
	defaultSessionTTL       = 24 * time.Hour
	defaultTimezone         = "Europe/Moscow"
)

// Config хранит всё, что нужно процессу для старта.
type Config struct {
	HTTPAddr string
	LogLevel slog.Level

	// DatabaseURL может быть пустым. Тогда сервис стартует без базы данных
	// и отвечает 503 на каждом доменном маршруте; /api/ping и /api/version
	// при этом продолжают работать, поэтому healthcheck контейнера и
	// проверка деплоя всё равно проходят.
	DatabaseURL      string
	DBMaxConns       int32
	DBConnectTimeout time.Duration
	RunMigrations    bool

	SessionTTL     time.Duration
	SecureCookies  bool
	AllowedOrigins []string

	Location *time.Location

	Bootstrap Bootstrap
}

// Bootstrap описывает первое агентство и пользователя, которые создаются,
// только пока в базе данных вообще нет ни одного пользователя.
type Bootstrap struct {
	AgencyName string
	Email      string
	Password   string
}

// Enabled сообщает, заданы ли все три значения для bootstrap.
func (b Bootstrap) Enabled() bool {
	return b.AgencyName != "" && b.Email != "" && b.Password != ""
}

// HasDatabase сообщает, настроен ли сервис на работу с базой данных.
func (c Config) HasDatabase() bool { return c.DatabaseURL != "" }

// Load читает конфигурацию из переменных окружения. Все проблемы
// сообщаются разом, а не по одной за запуск, поэтому неверно настроенный
// деплой можно исправить за один заход, а не за пять.
func Load() (Config, error) {
	var problems []error

	cfg := Config{
		HTTPAddr:         envString("HTTP_ADDR", defaultHTTPAddr),
		LogLevel:         logLevel(),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		DBMaxConns:       defaultDBMaxConns,
		DBConnectTimeout: defaultDBConnectTimeout,
		RunMigrations:    true,
		SessionTTL:       defaultSessionTTL,
		SecureCookies:    true,
		Bootstrap: Bootstrap{
			AgencyName: os.Getenv("BOOTSTRAP_AGENCY_NAME"),
			Email:      strings.TrimSpace(os.Getenv("BOOTSTRAP_USER_EMAIL")),
			Password:   os.Getenv("BOOTSTRAP_USER_PASSWORD"),
		},
	}

	maxConns, err := envInt("DB_MAX_CONNS", defaultDBMaxConns)
	switch {
	case err != nil:
		problems = append(problems, err)
	case maxConns < 1:
		problems = append(problems, errors.New("DB_MAX_CONNS must be at least 1"))
	default:
		cfg.DBMaxConns = int32(maxConns)
	}

	if timeout, err := envDuration("DB_CONNECT_TIMEOUT", defaultDBConnectTimeout); err != nil {
		problems = append(problems, err)
	} else if timeout <= 0 {
		problems = append(problems, errors.New("DB_CONNECT_TIMEOUT must be positive"))
	} else {
		cfg.DBConnectTimeout = timeout
	}

	if run, err := envBool("RUN_MIGRATIONS", true); err != nil {
		problems = append(problems, err)
	} else {
		cfg.RunMigrations = run
	}

	if ttl, err := envDuration("SESSION_TTL", defaultSessionTTL); err != nil {
		problems = append(problems, err)
	} else if ttl <= 0 {
		problems = append(problems, errors.New("SESSION_TTL must be positive"))
	} else {
		cfg.SessionTTL = ttl
	}

	if secure, err := envBool("SECURE_COOKIES", true); err != nil {
		problems = append(problems, err)
	} else {
		cfg.SecureCookies = secure
	}

	cfg.AllowedOrigins = envList("ALLOWED_ORIGINS")

	// В Alpine нет /usr/share/zoneinfo, поэтому это разрешается только
	// благодаря импорту time/tzdata в cmd/api. Не убирайте этот импорт
	// при правках main.
	name := envString("APP_TIMEZONE", defaultTimezone)
	location, err := time.LoadLocation(name)
	if err != nil {
		problems = append(problems, fmt.Errorf("APP_TIMEZONE %q is not a known timezone: %w", name, err))
	} else {
		cfg.Location = location
	}

	partial := cfg.Bootstrap.AgencyName != "" || cfg.Bootstrap.Email != "" || cfg.Bootstrap.Password != ""
	if partial && !cfg.Bootstrap.Enabled() {
		problems = append(problems, errors.New(
			"BOOTSTRAP_AGENCY_NAME, BOOTSTRAP_USER_EMAIL and BOOTSTRAP_USER_PASSWORD must be set together"))
	}

	if len(problems) > 0 {
		return Config{}, errors.Join(problems...)
	}

	return cfg, nil
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func envInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}

	return value, nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q", key, raw)
	}

	return value, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 24h or 30m, got %q", key, raw)
	}

	return value, nil
}

func envList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}

	return values
}

// logLevel читает LOG_LEVEL, по умолчанию возвращая info. Поставьте debug,
// чтобы увидеть пробы healthcheck — они намеренно логируются ниже info.
func logLevel() slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		return slog.LevelInfo
	}

	return level
}

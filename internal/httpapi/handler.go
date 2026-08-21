package httpapi

import (
	"log/slog"
	"net/http"
)

const (
	pingPath    = "/api/ping"
	versionPath = "/api/version"
	healthPath  = "/api/health"
)

type pingResponse struct {
	Message string `json:"message"`
}

type versionResponse struct {
	Commit string `json:"commit"`
}

// NewHandler возвращает обработчик HTTP API, сообщающий переданную версию
// сборки. Запросы логируются через logger; используемые уровни см. в withLogging.
//
// Нулевой Deps обслуживает только /api/ping и /api/version. Именно это
// делает healthcheck контейнера, проверку деплоя и их тесты независимыми от
// базы данных.
func NewHandler(logger *slog.Logger, version string, deps Deps) http.Handler {
	api := &api{deps: deps.withDefaults(), logger: logger}

	mux := http.NewServeMux()

	// Открытые маршруты. /api/ping и /api/version должны оставаться без
	// авторизации: HEALTHCHECK в Dockerfile и обе проверки деплоя обращаются к ним напрямую.
	mux.HandleFunc("GET "+pingPath, handlePing)
	mux.HandleFunc("GET "+versionPath, handleVersion(version))
	mux.HandleFunc("GET "+healthPath, api.handleHealth)
	mux.HandleFunc("POST /api/auth/login", api.handleLogin)
	mux.HandleFunc("POST /api/auth/register", api.handleRegister)
	mux.HandleFunc("POST /api/auth/verify-email", api.handleVerifyEmail)
	mux.HandleFunc("POST /api/auth/resend-verification", api.handleResendVerification)

	// Защищённые маршруты. Авторизация подключается к каждому маршруту по
	// отдельности, а не глобально, потому что у http.ServeMux нет групп
	// маршрутов, а два эндпоинта выше должны оставаться открытыми.
	protect := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, api.requireAuth(handler))
	}

	protect("POST /api/auth/logout", api.handleLogout)
	protect("GET /api/auth/session", api.handleSession)

	protect("GET /api/users", api.handleListUsers)
	protect("POST /api/users", api.handleCreateUser)
	protect("PATCH /api/users/{id}", api.handleUpdateUser)

	protect("GET /api/agency", api.handleGetAgency)

	protect("GET /api/references", api.handleReferences)

	protect("GET /api/acquisition-channels", api.handleListChannels)
	protect("POST /api/acquisition-channels", api.handleCreateChannel)
	protect("PATCH /api/acquisition-channels/{id}", api.handleUpdateChannel)
	protect("DELETE /api/acquisition-channels/{id}", api.handleDeleteChannel)

	protect("GET /api/partners", api.handleListPartners)
	protect("POST /api/partners", api.handleCreatePartner)
	protect("GET /api/partners/{id}", api.handleGetPartner)
	protect("PATCH /api/partners/{id}", api.handleUpdatePartner)
	protect("DELETE /api/partners/{id}", api.handleDeletePartner)

	protect("GET /api/tour-operators", api.handleListOperators)
	protect("POST /api/tour-operators", api.handleCreateOperator)
	protect("GET /api/tour-operators/{id}", api.handleGetOperator)
	protect("PATCH /api/tour-operators/{id}", api.handleUpdateOperator)
	protect("DELETE /api/tour-operators/{id}", api.handleDeleteOperator)

	protect("GET /api/payers", api.handleListPayers)
	protect("POST /api/payers", api.handleCreatePayer)
	protect("GET /api/payers/{id}", api.handleGetPayer)
	protect("PATCH /api/payers/{id}", api.handleUpdatePayer)
	protect("DELETE /api/payers/{id}", api.handleDeletePayer)

	protect("GET /api/tourists", api.handleListTourists)
	protect("POST /api/tourists", api.handleCreateTourist)
	protect("GET /api/tourists/{id}", api.handleGetTourist)
	protect("PATCH /api/tourists/{id}", api.handleUpdateTourist)
	protect("DELETE /api/tourists/{id}", api.handleDeleteTourist)
	protect("GET /api/tourists/{id}/applications", api.handleTouristApplications)
	protect("GET /api/tourists/{id}/history", api.handleTouristHistory)

	protect("GET /api/applications", api.handleListApplications)
	protect("POST /api/applications", api.handleCreateApplication)
	protect("GET /api/applications/{id}", api.handleGetApplication)
	protect("PATCH /api/applications/{id}", api.handleUpdateApplication)
	protect("DELETE /api/applications/{id}", api.handleDeleteApplication)
	protect("POST /api/applications/{id}/status", api.handleChangeStatus)
	protect("PUT /api/applications/{id}/tourists", api.handleSetApplicationTourists)
	protect("GET /api/applications/{id}/history", api.handleApplicationHistory)

	protect("GET /api/applications/{id}/deadlines", api.handleListDeadlines)
	protect("POST /api/applications/{id}/deadlines", api.handleCreateDeadline)
	protect("PATCH /api/applications/{id}/deadlines/{deadlineId}", api.handleUpdateDeadline)
	protect("DELETE /api/applications/{id}/deadlines/{deadlineId}", api.handleDeleteDeadline)

	protect("GET /api/applications/{id}/transactions", api.handleListApplicationTransactions)
	protect("POST /api/applications/{id}/transactions", api.handleCreateTransaction)
	protect("DELETE /api/applications/{id}/transactions/{transactionId}", api.handleVoidTransaction)
	protect("GET /api/applications/{id}/finance", api.handleApplicationFinance)

	protect("GET /api/transactions", api.handleListTransactions)
	protect("GET /api/reports/revenue", api.handleRevenueReport)
	protect("GET /api/reports/applications", api.handleApplicationFunnelReport)
	protect("GET /api/reports/directions", api.handleDirectionsReport)
	protect("GET /api/reports/tour-operators", api.handleOperatorsReport)
	protect("GET /api/reports/channels", api.handleChannelsReport)
	protect("GET /api/reports/repeat-customers", api.handleRepeatCustomersReport)

	protect("GET /api/reminders", api.handleReminders)

	// Нет общего маршрута "/": его регистрация имела бы приоритет над
	// шаблонами методов выше и превратила бы POST /api/ping в 200 вместо
	// 405, который сейчас отдаёт mux.
	return withLogging(logger, withRecovery(logger, mux))
}

func handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, pingResponse{Message: "pong"})
}

func handleVersion(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, versionResponse{Commit: version})
	}
}

// handleHealth сообщает, доступна ли база данных. Намеренно отделён от
// /api/ping: ping обслуживает healthcheck контейнера и проверку деплоя, и не
// должен начать падать из-за того, что база данных на мгновение моргнула.
func (a *api) handleHealth(w http.ResponseWriter, r *http.Request) {
	if a.deps.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "degraded",
			"database": "not configured",
		})

		return
	}

	if err := a.deps.Store.Ping(r.Context()); err != nil {
		a.logger.LogAttrs(r.Context(), slog.LevelError, "health check failed", slog.String("error", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "degraded",
			"database": "unreachable",
		})

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "database": "ok"})
}

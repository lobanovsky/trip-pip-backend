package httpapi

import (
	"net/http"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// reportDateRange читает необязательные from/to, общие для всех отчётов
// аналитики: подставляет период с начала текущего года агентства по сегодня
// и ограничивает диапазон maxRevenueRangeDays (см. payment_transactions.go) —
// тем же лимитом, что у /api/reports/revenue, который тоже использует этот
// хелпер.
func (a *api) reportDateRange(r *http.Request) (from, to store.Date, fieldErrs map[string]string) {
	today := a.today()
	from = store.Date{Year: today.Year, Month: 1, Day: 1}
	to = today
	fieldErrs = map[string]string{}

	if parsed, err := dateQuery(r, "from"); err != nil {
		fieldErrs["from"] = err.Error()
	} else if parsed != nil {
		from = *parsed
	}

	if parsed, err := dateQuery(r, "to"); err != nil {
		fieldErrs["to"] = err.Error()
	} else if parsed != nil {
		to = *parsed
	}

	if len(fieldErrs) > 0 {
		return from, to, fieldErrs
	}

	if to.Before(from) {
		fieldErrs["to"] = "не может быть раньше from"
	} else if to.DaysUntil(from) > maxRevenueRangeDays {
		fieldErrs["to"] = "диапазон не может быть больше пяти лет"
	}

	return from, to, fieldErrs
}

// reportLimit читает необязательный top-N лимит отчётов-рейтингов
// (направления, туроператоры, каналы, повторные клиенты).
func reportLimit(r *http.Request) int {
	return intQuery(r, "limit", 10, 50)
}

func (a *api) handleApplicationFunnelReport(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	from, to, fieldErrs := a.reportDateRange(r)
	if len(fieldErrs) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса", fieldErrs)

		return
	}

	funnel, err := a.deps.Store.ApplicationFunnel(r.Context(), identity.AgencyID, from, to)
	if err != nil {
		a.writeStoreError(w, r, "application funnel report", err)

		return
	}

	writeJSON(w, http.StatusOK, funnel)
}

func (a *api) handleDirectionsReport(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	from, to, fieldErrs := a.reportDateRange(r)
	if len(fieldErrs) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса", fieldErrs)

		return
	}

	limit := reportLimit(r)
	stats, err := a.deps.Store.DirectionStats(r.Context(), identity.AgencyID, from, to, limit)
	if err != nil {
		a.writeStoreError(w, r, "directions report", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.DirectionStat]{Items: stats, Total: len(stats), Limit: limit})
}

func (a *api) handleOperatorsReport(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	from, to, fieldErrs := a.reportDateRange(r)
	if len(fieldErrs) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса", fieldErrs)

		return
	}

	limit := reportLimit(r)
	stats, err := a.deps.Store.OperatorStats(r.Context(), identity.AgencyID, from, to, limit)
	if err != nil {
		a.writeStoreError(w, r, "operators report", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.OperatorStat]{Items: stats, Total: len(stats), Limit: limit})
}

func (a *api) handleChannelsReport(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	from, to, fieldErrs := a.reportDateRange(r)
	if len(fieldErrs) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса", fieldErrs)

		return
	}

	limit := reportLimit(r)
	stats, err := a.deps.Store.ChannelStats(r.Context(), identity.AgencyID, from, to, limit)
	if err != nil {
		a.writeStoreError(w, r, "channels report", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.ChannelStat]{Items: stats, Total: len(stats), Limit: limit})
}

func (a *api) handleRepeatCustomersReport(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	from, to, fieldErrs := a.reportDateRange(r)
	if len(fieldErrs) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса", fieldErrs)

		return
	}

	report, err := a.deps.Store.RepeatCustomerReport(r.Context(), identity.AgencyID, from, to, reportLimit(r))
	if err != nil {
		a.writeStoreError(w, r, "repeat customers report", err)

		return
	}

	writeJSON(w, http.StatusOK, report)
}

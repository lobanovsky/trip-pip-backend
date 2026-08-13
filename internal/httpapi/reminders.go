package httpapi

import "net/http"

// handleReminders отвечает на вопрос «что скоро потребует внимания»:
// заканчивающиеся сроки документов и невыполненные обязательства по
// активным заявкам.
//
// Просроченные пункты не отфильтровываются, а включаются, потому что именно
// они реально стоят агентству сорванной поездки.
func (a *api) handleReminders(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	days := intQuery(r, "withinDays", 90, 3650)
	today := a.today()
	before := today.AddDays(days)
	limit, _ := paging(r)

	documents, err := a.deps.Store.ExpiringDocuments(r.Context(), identity.AgencyID, before, today, limit)
	if err != nil {
		a.writeStoreError(w, r, "list expiring documents", err)

		return
	}

	deadlines, err := a.deps.Store.UpcomingDeadlines(r.Context(), identity.AgencyID, before, today, limit)
	if err != nil {
		a.writeStoreError(w, r, "list upcoming deadlines", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"withinDays":        days,
		"today":             today,
		"expiringDocuments": documents,
		"upcomingDeadlines": deadlines,
	})
}

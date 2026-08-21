package httpapi

import "net/http"

// handleGetAgency возвращает данные агентства текущего пользователя.
func (a *api) handleGetAgency(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	agency, err := a.deps.Store.Agency(r.Context(), identity.AgencyID)
	if err != nil {
		a.writeStoreError(w, r, "load agency", err)

		return
	}

	writeJSON(w, http.StatusOK, agency)
}

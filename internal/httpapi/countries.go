package httpapi

import (
	"net/http"
	"strings"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// Справочник стран -------------------------------------------------------------

func (a *api) handleListCountries(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	countries, err := a.deps.Store.ListCountries(r.Context(), identity.AgencyID)
	if err != nil {
		a.writeStoreError(w, r, "list countries", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Country]{
		Items: countries, Total: len(countries), Limit: len(countries),
	})
}

func (a *api) handleUpdateCountryVisibility(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	// pathID тут не подходит: он проверяет UUID, а код страны — две буквы.
	code := strings.ToUpper(r.PathValue("code"))
	if len(code) != 2 {
		writeError(w, r, http.StatusNotFound, codeNotFound, "Запись не найдена")

		return
	}

	var patch struct {
		IsActive  *bool `json:"isActive"`
		SortOrder *int  `json:"sortOrder"`
	}
	if err := decodeJSON(w, r, &patch); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	country, err := a.deps.Store.UpdateCountryVisibility(r.Context(), identity.AgencyID, code, patch.IsActive, patch.SortOrder)
	if err != nil {
		a.writeStoreError(w, r, "update country visibility", err)

		return
	}

	writeJSON(w, http.StatusOK, country)
}

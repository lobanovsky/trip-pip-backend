package httpapi

import (
	"net/http"
	"strings"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// handleReferences возвращает фиксированные словари, нужные фронтенду для
// отрисовки выпадающих списков, — так эти списки живут в одном месте, а не
// переписываются в интерфейсе заново.
func (a *api) handleReferences(w http.ResponseWriter, r *http.Request) {
	transitions := make(map[string][]string, len(store.AllStatuses))
	for _, status := range store.AllStatuses {
		transitions[status] = store.AllowedTransitions(status)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"applicationStatuses":        store.AllStatuses,
		"statusTransitions":          transitions,
		"deadlineKinds":              store.DeadlineKinds,
		"payerKinds":                 []string{store.PayerIndividual, store.PayerCompany},
		"partnerKinds":               []string{"person", "company"},
		"transactionKinds":           store.AllTransactionKinds,
		"paymentMethods":             store.AllPaymentMethods,
		"applicationPaymentStatuses": store.AllPaymentStatuses,
		"genders":                    []string{"male", "female"},
		"touristSortOptions":         []string{"lastName", "-lastName", "createdAt", "-createdAt", "updatedAt", "-updatedAt"},
		"applicationSortFields":      []string{"number", "-number", "createdAt", "-createdAt", "updatedAt", "-updatedAt", "departDate", "-departDate", "upcomingDepartDate"},
	})
}

// Каналы привлечения -----------------------------------------------------------

type channelRequest struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	SortOrder int     `json:"sortOrder"`
	IsActive  *bool   `json:"isActive"`
	NamePatch *string `json:"-"`
}

func (a *api) handleListChannels(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	channels, err := a.deps.Store.ListChannels(r.Context(), identity.AgencyID, boolQuery(r, "activeOnly"))
	if err != nil {
		a.writeStoreError(w, r, "list channels", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.AcquisitionChannel]{
		Items: channels, Total: len(channels), Limit: len(channels),
	})
}

func (a *api) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var request channelRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	request.Code = strings.ToLower(strings.TrimSpace(request.Code))
	request.Name = strings.TrimSpace(request.Name)

	fields := map[string]string{}
	if !channelCodePattern(request.Code) {
		fields["code"] = "код — латинские строчные буквы, цифры и подчёркивание"
	}
	if request.Name == "" {
		fields["name"] = "обязательное поле"
	}
	if len(fields) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей", fields)

		return
	}

	channel, err := a.deps.Store.CreateChannel(r.Context(), identity.AgencyID, request.Code, request.Name, request.SortOrder)
	if err != nil {
		a.writeStoreError(w, r, "create channel", err)

		return
	}

	writeJSON(w, http.StatusCreated, channel)
}

func (a *api) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	var patch struct {
		Name      *string `json:"name"`
		IsActive  *bool   `json:"isActive"`
		SortOrder *int    `json:"sortOrder"`
	}
	if err := decodeJSON(w, r, &patch); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	channel, err := a.deps.Store.UpdateChannel(r.Context(), identity.AgencyID, id, patch.Name, patch.IsActive, patch.SortOrder)
	if err != nil {
		a.writeStoreError(w, r, "update channel", err)

		return
	}

	writeJSON(w, http.StatusOK, channel)
}

func (a *api) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	if err := a.deps.Store.DeleteChannel(r.Context(), identity.AgencyID, id); err != nil {
		a.writeStoreError(w, r, "delete channel", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func channelCodePattern(code string) bool {
	if code == "" || len(code) > 32 {
		return false
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}

	return true
}

// Партнёры ----------------------------------------------------------------------

func (a *api) handleListPartners(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	partners, total, err := a.deps.Store.ListPartners(r.Context(), identity.AgencyID, searchQuery(r), limit, offset)
	if err != nil {
		a.writeStoreError(w, r, "list partners", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Partner]{Items: partners, Total: total, Limit: limit, Offset: offset})
}

func (a *api) handleGetPartner(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	partner, err := a.deps.Store.Partner(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load partner", err)

		return
	}

	writeJSON(w, http.StatusOK, partner)
}

func (a *api) handleCreatePartner(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var input store.PartnerInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate partner", err)

		return
	}

	partner, err := a.deps.Store.CreatePartner(r.Context(), identity.AgencyID, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "create partner", err)

		return
	}

	writeJSON(w, http.StatusCreated, partner)
}

func (a *api) handleUpdatePartner(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	existing, err := a.deps.Store.Partner(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load partner", err)

		return
	}

	// Декодирование поверх сохранённых значений даёт семантику PATCH бесплатно:
	// поле, которое клиент не прислал, сохраняет своё значение, явный null его очищает.
	input := store.PartnerAsInput(existing)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate partner", err)

		return
	}

	partner, err := a.deps.Store.UpdatePartner(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "update partner", err)

		return
	}

	writeJSON(w, http.StatusOK, partner)
}

func (a *api) handleDeletePartner(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	if err := a.deps.Store.ArchivePartner(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context()))); err != nil {
		a.writeStoreError(w, r, "archive partner", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Туроператоры ------------------------------------------------------------------

func (a *api) handleListOperators(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	operators, total, err := a.deps.Store.ListOperators(r.Context(), identity.AgencyID, searchQuery(r), limit, offset)
	if err != nil {
		a.writeStoreError(w, r, "list operators", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.TourOperator]{Items: operators, Total: total, Limit: limit, Offset: offset})
}

func (a *api) handleGetOperator(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	operator, err := a.deps.Store.TourOperator(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load operator", err)

		return
	}

	writeJSON(w, http.StatusOK, operator)
}

func (a *api) handleCreateOperator(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var input store.TourOperatorInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate operator", err)

		return
	}

	operator, err := a.deps.Store.CreateOperator(r.Context(), identity.AgencyID, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "create operator", err)

		return
	}

	writeJSON(w, http.StatusCreated, operator)
}

func (a *api) handleUpdateOperator(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	existing, err := a.deps.Store.TourOperator(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load operator", err)

		return
	}

	input := store.TourOperatorAsInput(existing)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate operator", err)

		return
	}

	operator, err := a.deps.Store.UpdateOperator(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "update operator", err)

		return
	}

	writeJSON(w, http.StatusOK, operator)
}

func (a *api) handleDeleteOperator(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	if err := a.deps.Store.ArchiveOperator(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context()))); err != nil {
		a.writeStoreError(w, r, "archive operator", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Плательщики -------------------------------------------------------------------

func (a *api) handleListPayers(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	kind := r.URL.Query().Get("kind")
	if kind != store.PayerIndividual && kind != store.PayerCompany {
		kind = ""
	}

	payers, total, err := a.deps.Store.ListPayers(r.Context(), identity.AgencyID, kind, searchQuery(r), limit, offset)
	if err != nil {
		a.writeStoreError(w, r, "list payers", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Payer]{Items: payers, Total: total, Limit: limit, Offset: offset})
}

func (a *api) handleGetPayer(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	payer, err := a.deps.Store.Payer(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load payer", err)

		return
	}

	writeJSON(w, http.StatusOK, payer)
}

func (a *api) handleCreatePayer(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var input store.PayerInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate payer", err)

		return
	}

	payer, err := a.deps.Store.CreatePayer(r.Context(), identity.AgencyID, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "create payer", err)

		return
	}

	writeJSON(w, http.StatusCreated, payer)
}

func (a *api) handleUpdatePayer(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	existing, err := a.deps.Store.Payer(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load payer", err)

		return
	}

	input := store.PayerAsInput(existing)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate payer", err)

		return
	}

	payer, err := a.deps.Store.UpdatePayer(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "update payer", err)

		return
	}

	writeJSON(w, http.StatusOK, payer)
}

func (a *api) handleDeletePayer(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	if err := a.deps.Store.ArchivePayer(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context()))); err != nil {
		a.writeStoreError(w, r, "archive payer", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// createApplicationRequest несёт заявку вместе с путешественниками, которые
// записываются в той же транзакции, что и сама заявка.
type createApplicationRequest struct {
	store.ApplicationInput
	TouristIDs []string `json:"touristIds"`
}

func (a *api) handleListApplications(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	departFrom, err := dateQuery(r, "departFrom")
	if err != nil {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса",
			map[string]string{"departFrom": err.Error()})

		return
	}

	departTo, err := dateQuery(r, "departTo")
	if err != nil {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса",
			map[string]string{"departTo": err.Error()})

		return
	}

	filter := store.ApplicationFilter{
		Search:        searchQuery(r),
		Statuses:      statusQuery(r),
		TouristID:     uuidQuery(r, "touristId"),
		OperatorID:    uuidQuery(r, "tourOperatorId"),
		ChannelID:     uuidQuery(r, "channelId"),
		ManagerID:     uuidQuery(r, "managerUserId"),
		DepartFrom:    departFrom,
		DepartTo:      departTo,
		PaymentStatus: paymentStatusQuery(r),
		Sort:          r.URL.Query().Get("sort"),
		Limit:         limit,
		Offset:        offset,
	}

	applications, total, err := a.deps.Store.ListApplications(r.Context(), identity.AgencyID, filter)
	if err != nil {
		a.writeStoreError(w, r, "list applications", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Application]{
		Items: applications, Total: total, Limit: limit, Offset: offset,
	})
}

func (a *api) handleGetApplication(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	application, err := a.deps.Store.Application(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load application", err)

		return
	}

	writeJSON(w, http.StatusOK, application)
}

func (a *api) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var request createApplicationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	request.Normalize()
	if err := request.Validate(); err != nil {
		a.writeStoreError(w, r, "validate application", err)

		return
	}

	if fields := invalidIDs(request.CustomerTouristID, request.TouristIDs); len(fields) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей", fields)

		return
	}

	application, err := a.deps.Store.CreateApplication(r.Context(), identity.AgencyID,
		identity.Actor(RequestID(r.Context())), request.ApplicationInput, request.TouristIDs)
	if err != nil {
		a.writeStoreError(w, r, "create application", err)

		return
	}

	writeJSON(w, http.StatusCreated, application)
}

func (a *api) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	existing, err := a.deps.Store.Application(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load application", err)

		return
	}

	input := store.ApplicationAsInput(existing)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate application", err)

		return
	}

	if !isUUID(input.CustomerTouristID) {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей",
			map[string]string{"customerTouristId": "некорректный идентификатор"})

		return
	}

	expectedVersion := intQuery(r, "version", 0, 1<<30)

	application, err := a.deps.Store.UpdateApplication(r.Context(), identity.AgencyID, id,
		identity.Actor(RequestID(r.Context())), input, expectedVersion)
	if err != nil {
		a.writeStoreError(w, r, "update application", err)

		return
	}

	writeJSON(w, http.StatusOK, application)
}

func (a *api) handleDeleteApplication(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	if err := a.deps.Store.ArchiveApplication(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context()))); err != nil {
		a.writeStoreError(w, r, "archive application", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type statusRequest struct {
	Status       string  `json:"status"`
	CancelReason *string `json:"cancelReason"`
}

func (a *api) handleChangeStatus(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	var request statusRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	request.Status = strings.TrimSpace(request.Status)
	if !isKnownStatus(request.Status) {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей",
			map[string]string{"status": "недопустимый статус"})

		return
	}

	application, err := a.deps.Store.ChangeStatus(r.Context(), identity.AgencyID, id,
		identity.Actor(RequestID(r.Context())), request.Status, request.CancelReason)
	if err != nil {
		a.writeStoreError(w, r, "change status", err)

		return
	}

	writeJSON(w, http.StatusOK, application)
}

type setTouristsRequest struct {
	TouristIDs []string `json:"touristIds"`
}

func (a *api) handleSetApplicationTourists(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	var request setTouristsRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	for _, touristID := range request.TouristIDs {
		if !isUUID(touristID) {
			writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей",
				map[string]string{"touristIds": "некорректный идентификатор туриста"})

			return
		}
	}

	tourists, err := a.deps.Store.SetApplicationTourists(r.Context(), identity.AgencyID, id,
		identity.Actor(RequestID(r.Context())), request.TouristIDs)
	if err != nil {
		a.writeStoreError(w, r, "set application tourists", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tourists": tourists})
}

// Сроки -----------------------------------------------------------------------

type deadlineRequest struct {
	Kind    string      `json:"kind"`
	DueDate *store.Date `json:"dueDate"`
	Note    *string     `json:"note"`
}

func (a *api) handleListDeadlines(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	deadlines, err := a.deps.Store.ListDeadlines(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "list deadlines", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Deadline]{
		Items: deadlines, Total: len(deadlines), Limit: len(deadlines),
	})
}

func (a *api) handleCreateDeadline(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	var request deadlineRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	fields := map[string]string{}
	if !isKnownDeadlineKind(request.Kind) {
		fields["kind"] = "недопустимый вид срока"
	}
	if request.DueDate == nil || request.DueDate.IsZero() {
		fields["dueDate"] = "обязательное поле"
	}
	if len(fields) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей", fields)

		return
	}

	deadline, err := a.deps.Store.CreateDeadline(r.Context(), identity.AgencyID, id, request.Kind, *request.DueDate, request.Note)
	if err != nil {
		a.writeStoreError(w, r, "create deadline", err)

		return
	}

	writeJSON(w, http.StatusCreated, deadline)
}

func (a *api) handleUpdateDeadline(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	deadlineID, ok := a.pathID(w, r, "deadlineId")
	if !ok {
		return
	}

	var patch struct {
		DueDate   *store.Date `json:"dueDate"`
		Note      *string     `json:"note"`
		Completed *bool       `json:"completed"`
	}
	if err := decodeJSON(w, r, &patch); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	deadline, err := a.deps.Store.UpdateDeadline(r.Context(), identity.AgencyID, deadlineID, patch.DueDate, patch.Note, patch.Completed)
	if err != nil {
		a.writeStoreError(w, r, "update deadline", err)

		return
	}

	writeJSON(w, http.StatusOK, deadline)
}

func (a *api) handleDeleteDeadline(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	deadlineID, ok := a.pathID(w, r, "deadlineId")
	if !ok {
		return
	}

	if err := a.deps.Store.DeleteDeadline(r.Context(), identity.AgencyID, deadlineID); err != nil {
		a.writeStoreError(w, r, "delete deadline", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func invalidIDs(customerID string, touristIDs []string) map[string]string {
	fields := map[string]string{}
	if !isUUID(customerID) {
		fields["customerTouristId"] = "некорректный идентификатор"
	}
	for _, id := range touristIDs {
		if !isUUID(id) {
			fields["touristIds"] = "некорректный идентификатор туриста"

			break
		}
	}

	return fields
}

func isKnownStatus(status string) bool {
	return slices.Contains(store.AllStatuses, status)
}

func isKnownDeadlineKind(kind string) bool {
	return slices.Contains(store.DeadlineKinds, kind)
}

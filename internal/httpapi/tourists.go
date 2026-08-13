package httpapi

import (
	"net/http"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

func (a *api) handleListTourists(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	expiringBefore, err := dateQuery(r, "expiringBefore")
	if err != nil {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса",
			map[string]string{"expiringBefore": err.Error()})

		return
	}

	// expiringInDays — форма, которую реально использует интерфейс; она
	// вычисляется относительно «сегодня» в часовом поясе агентства, а не сервера.
	if days := intQuery(r, "expiringInDays", -1, 3650); days >= 0 && expiringBefore == nil {
		cutoff := a.today().AddDays(days)
		expiringBefore = &cutoff
	}

	filter := store.TouristFilter{
		Search:         searchQuery(r),
		ChannelID:      uuidQuery(r, "channelId"),
		PartnerID:      uuidQuery(r, "partnerId"),
		ExpiringBefore: expiringBefore,
		Sort:           r.URL.Query().Get("sort"),
		Limit:          limit,
		Offset:         offset,
	}

	tourists, total, err := a.deps.Store.ListTourists(r.Context(), identity.AgencyID, filter)
	if err != nil {
		a.writeStoreError(w, r, "list tourists", err)

		return
	}

	// В списках номера документов маскируются. Иначе просматриваемый список
	// разбрасывал бы полные паспортные данные по экранам, кэшам и
	// скриншотам; эндпоинт деталей отдаёт их полностью, когда карточка
	// действительно открывается.
	items := make([]store.Tourist, 0, len(tourists))
	for _, tourist := range tourists {
		items = append(items, tourist.Masked())
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Tourist]{Items: items, Total: total, Limit: limit, Offset: offset})
}

func (a *api) handleGetTourist(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	tourist, err := a.deps.Store.Tourist(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load tourist", err)

		return
	}

	writeJSON(w, http.StatusOK, tourist)
}

func (a *api) handleCreateTourist(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	var input store.TouristInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(a.today()); err != nil {
		a.writeStoreError(w, r, "validate tourist", err)

		return
	}

	tourist, err := a.deps.Store.CreateTourist(r.Context(), identity.AgencyID, identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "create tourist", err)

		return
	}

	writeJSON(w, http.StatusCreated, tourist)
}

func (a *api) handleUpdateTourist(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	existing, err := a.deps.Store.Tourist(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load tourist", err)

		return
	}

	// Декодирование поверх сохранённых значений даёт семантику PATCH бесплатно:
	// поле, которое клиент не прислал, сохраняет своё значение, явный null его очищает.
	input := store.TouristAsInput(existing)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	input.Normalize()
	if err := input.Validate(a.today()); err != nil {
		a.writeStoreError(w, r, "validate tourist", err)

		return
	}

	// Вызывающая сторона, присылающая версию, участвует в обнаружении
	// конфликтов; если версии нет, сохраняется старое поведение — побеждает
	// последняя запись.
	expectedVersion := intQuery(r, "version", 0, 1<<30)

	tourist, err := a.deps.Store.UpdateTourist(r.Context(), identity.AgencyID, id,
		identity.Actor(RequestID(r.Context())), input, expectedVersion)
	if err != nil {
		a.writeStoreError(w, r, "update tourist", err)

		return
	}

	writeJSON(w, http.StatusOK, tourist)
}

func (a *api) handleDeleteTourist(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	if err := a.deps.Store.ArchiveTourist(r.Context(), identity.AgencyID, id, identity.Actor(RequestID(r.Context()))); err != nil {
		a.writeStoreError(w, r, "archive tourist", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleTouristApplications обслуживает список «связанных заявок» на карточке.
func (a *api) handleTouristApplications(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	limit, offset := paging(r)
	filter := store.ApplicationFilter{
		TouristID: id,
		Sort:      "-createdAt",
		Limit:     limit,
		Offset:    offset,
	}

	applications, total, err := a.deps.Store.ListApplications(r.Context(), identity.AgencyID, filter)
	if err != nil {
		a.writeStoreError(w, r, "list tourist applications", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Application]{
		Items: applications, Total: total, Limit: limit, Offset: offset,
	})
}

func (a *api) handleTouristHistory(w http.ResponseWriter, r *http.Request) {
	a.writeHistory(w, r, store.EntityTourist)
}

func (a *api) handleApplicationHistory(w http.ResponseWriter, r *http.Request) {
	a.writeHistory(w, r, store.EntityApplication)
}

func (a *api) writeHistory(w http.ResponseWriter, r *http.Request, entityType string) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	limit, offset := paging(r)

	entries, total, err := a.deps.Store.ListHistory(r.Context(), identity.AgencyID, entityType, id, limit, offset)
	if err != nil {
		a.writeStoreError(w, r, "list history", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.ChangeEntry]{
		Items: entries, Total: total, Limit: limit, Offset: offset,
	})
}

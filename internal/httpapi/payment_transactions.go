package httpapi

import (
	"net/http"
	"slices"

	"github.com/lobanovsky/trip-pip-backend/internal/store"
)

// Транзакции ----------------------------------------------------------------

func (a *api) handleListApplicationTransactions(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	transactions, err := a.deps.Store.ListApplicationTransactions(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "list application transactions", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Transaction]{
		Items: transactions, Total: len(transactions), Limit: len(transactions),
	})
}

func (a *api) handleCreateTransaction(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	var input store.TransactionInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	// Дата операции по умолчанию — сегодня по часовому поясу агентства:
	// платёж обычно заносится в систему в день, когда он произошёл.
	if input.OccurredAt.IsZero() {
		input.OccurredAt = a.today()
	}

	input.Normalize()
	if err := input.Validate(); err != nil {
		a.writeStoreError(w, r, "validate transaction", err)

		return
	}

	if fields := invalidTransactionRefs(input); len(fields) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте заполнение полей", fields)

		return
	}

	transaction, err := a.deps.Store.CreateTransaction(r.Context(), identity.AgencyID, id,
		identity.Actor(RequestID(r.Context())), input)
	if err != nil {
		a.writeStoreError(w, r, "create transaction", err)

		return
	}

	writeJSON(w, http.StatusCreated, transaction)
}

func (a *api) handleVoidTransaction(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	transactionID, ok := a.pathID(w, r, "transactionId")
	if !ok {
		return
	}

	if err := a.deps.Store.VoidTransaction(r.Context(), identity.AgencyID, transactionID,
		identity.Actor(RequestID(r.Context()))); err != nil {
		a.writeStoreError(w, r, "void transaction", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *api) handleApplicationFinance(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	id, ok := a.pathID(w, r, "id")
	if !ok {
		return
	}

	balance, err := a.deps.Store.ApplicationBalance(r.Context(), identity.AgencyID, id)
	if err != nil {
		a.writeStoreError(w, r, "load application finance", err)

		return
	}

	writeJSON(w, http.StatusOK, balance)
}

func (a *api) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())
	limit, offset := paging(r)

	occurredFrom, err := dateQuery(r, "occurredFrom")
	if err != nil {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса",
			map[string]string{"occurredFrom": err.Error()})

		return
	}

	occurredTo, err := dateQuery(r, "occurredTo")
	if err != nil {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса",
			map[string]string{"occurredTo": err.Error()})

		return
	}

	filter := store.TransactionFilter{
		Kind:           transactionKindQuery(r),
		ApplicationID:  uuidQuery(r, "applicationId"),
		PayerID:        uuidQuery(r, "payerId"),
		TourOperatorID: uuidQuery(r, "tourOperatorId"),
		OccurredFrom:   occurredFrom,
		OccurredTo:     occurredTo,
		Limit:          limit,
		Offset:         offset,
	}

	transactions, total, err := a.deps.Store.ListTransactions(r.Context(), identity.AgencyID, filter)
	if err != nil {
		a.writeStoreError(w, r, "list transactions", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.Transaction]{
		Items: transactions, Total: total, Limit: limit, Offset: offset,
	})
}

func invalidTransactionRefs(in store.TransactionInput) map[string]string {
	fields := map[string]string{}
	if in.PayerID != nil && !isUUID(*in.PayerID) {
		fields["payerId"] = "некорректный идентификатор"
	}
	if in.TourOperatorID != nil && !isUUID(*in.TourOperatorID) {
		fields["tourOperatorId"] = "некорректный идентификатор"
	}

	return fields
}

// Отчёты ----------------------------------------------------------------------

// revenueUnits — периоды, поддерживаемые базовым финансовым отчётом.
var revenueUnits = []string{"month", "quarter", "year"}

// maxRevenueRangeDays ограничивает диапазон одного запроса отчёта, чтобы
// нельзя было заказать агрегацию по всей истории агентства без пагинации.
const maxRevenueRangeDays = 5 * 366

func (a *api) handleRevenueReport(w http.ResponseWriter, r *http.Request) {
	identity, _ := Identity(r.Context())

	unit := r.URL.Query().Get("unit")
	if !slices.Contains(revenueUnits, unit) {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса",
			map[string]string{"unit": "допустимые значения: month, quarter, year"})

		return
	}

	from, to, fieldErrs := a.reportDateRange(r)
	if len(fieldErrs) > 0 {
		writeErrorFields(w, r, http.StatusBadRequest, codeValidation, "Проверьте параметры запроса", fieldErrs)

		return
	}

	revenue, err := a.deps.Store.RevenueByPeriod(r.Context(), identity.AgencyID, unit, from, to)
	if err != nil {
		a.writeStoreError(w, r, "revenue report", err)

		return
	}

	writeJSON(w, http.StatusOK, listEnvelope[store.PeriodRevenue]{
		Items: revenue, Total: len(revenue), Limit: len(revenue),
	})
}

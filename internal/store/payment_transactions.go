package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Виды финансовых транзакций.
const (
	TransactionReceipt          = "receipt"           // поступление от плательщика
	TransactionOperatorTransfer = "operator_transfer" // перечисление туроператору
	TransactionRefund           = "refund"            // возврат плательщику
	TransactionBonusIncome      = "bonus_income"      // дополнительная выгода от туроператора
)

// AllTransactionKinds перечисляет виды транзакций, используется эндпоинтом справочников.
var AllTransactionKinds = []string{
	TransactionReceipt, TransactionOperatorTransfer, TransactionRefund, TransactionBonusIncome,
}

// Способы оплаты.
const (
	PaymentMethodCash      = "cash"
	PaymentMethodTransfer  = "bank_transfer"
	PaymentMethodAcquiring = "card_acquiring"
)

// AllPaymentMethods перечисляет способы оплаты, используется эндпоинтом справочников.
var AllPaymentMethods = []string{PaymentMethodCash, PaymentMethodTransfer, PaymentMethodAcquiring}

// Статусы оплаты заявки — производная величина от баланса, нигде не хранится.
const (
	PaymentStatusUnpaid   = "unpaid"
	PaymentStatusPartial  = "partial"
	PaymentStatusPaid     = "paid"
	PaymentStatusOverpaid = "overpaid"
)

// AllPaymentStatuses перечисляет статусы оплаты, используется эндпоинтом справочников.
var AllPaymentStatuses = []string{PaymentStatusUnpaid, PaymentStatusPartial, PaymentStatusPaid, PaymentStatusOverpaid}

// Transaction — один факт движения денег по заявке. Агентское вознаграждение
// здесь не хранится отдельной строкой: оно не факт движения денег, а разница
// между стоимостью поездки и суммой, ушедшей туроператору, — считается на
// лету в ApplicationBalance/RevenueByPeriod.
type Transaction struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Kind          string `json:"kind"`
	Amount        string `json:"amount"`

	PayerID        *string `json:"payerId,omitempty"`
	TourOperatorID *string `json:"tourOperatorId,omitempty"`

	PaymentMethod string  `json:"paymentMethod"`
	FeeAmount     *string `json:"feeAmount,omitempty"`

	OccurredAt Date    `json:"occurredAt"`
	Note       *string `json:"note,omitempty"`

	CreatedBy  *string    `json:"createdBy,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
}

// TransactionInput — данные для регистрации транзакции. Транзакции не
// редактируются после создания, только аннулируются VoidTransaction, поэтому
// здесь нет отдельного *AsInput для PATCH-слияния, как у других сущностей.
type TransactionInput struct {
	Kind   string `json:"kind" history:"kind"`
	Amount string `json:"amount" history:"amount"`

	PayerID        *string `json:"payerId" history:"payerId"`
	TourOperatorID *string `json:"tourOperatorId" history:"tourOperatorId"`

	PaymentMethod string  `json:"paymentMethod" history:"paymentMethod"`
	FeeAmount     *string `json:"feeAmount" history:"feeAmount"`

	OccurredAt Date    `json:"occurredAt" history:"occurredAt"`
	Note       *string `json:"note" history:"note"`
}

// Normalize обрезает пробелы и очищает поля, не относящиеся к выбранному
// виду транзакции, — так форма, переключённая между видами, не оставляет
// после себя ссылку на плательщика у перевода туроператору или наоборот.
func (in *TransactionInput) Normalize() {
	in.Kind = strings.TrimSpace(in.Kind)
	in.PaymentMethod = strings.TrimSpace(in.PaymentMethod)
	in.Amount = strings.TrimSpace(in.Amount)
	trimPtr(&in.FeeAmount)
	trimPtr(&in.Note)

	if in.OccurredAt.IsZero() {
		in.OccurredAt = NewDate(time.Now())
	}

	switch in.Kind {
	case TransactionReceipt, TransactionRefund:
		in.TourOperatorID = nil
	case TransactionOperatorTransfer, TransactionBonusIncome:
		in.PayerID = nil
	}

	if in.PaymentMethod != PaymentMethodAcquiring {
		in.FeeAmount = nil
	}
}

// Validate сообщает обо всех проблемах сразу.
func (in TransactionInput) Validate() error {
	v := newValidator()
	v.oneOf("kind", in.Kind, TransactionReceipt, TransactionOperatorTransfer, TransactionRefund, TransactionBonusIncome)
	v.oneOf("paymentMethod", in.PaymentMethod, PaymentMethodCash, PaymentMethodTransfer, PaymentMethodAcquiring)

	switch in.Kind {
	case TransactionReceipt, TransactionRefund:
		if in.PayerID == nil || *in.PayerID == "" {
			v.add("payerId", "обязательное поле для этого вида транзакции")
		}
	case TransactionOperatorTransfer, TransactionBonusIncome:
		if in.TourOperatorID == nil || *in.TourOperatorID == "" {
			v.add("tourOperatorId", "обязательное поле для этого вида транзакции")
		}
	}

	switch amount, err := strconv.ParseFloat(in.Amount, 64); {
	case in.Amount == "":
		v.add("amount", "обязательное поле")
	case err != nil:
		v.add("amount", "сумма должна быть числом")
	case amount <= 0:
		v.add("amount", "сумма должна быть положительной")
	}

	if in.FeeAmount != nil && *in.FeeAmount != "" {
		switch fee, err := strconv.ParseFloat(*in.FeeAmount, 64); {
		case err != nil:
			v.add("feeAmount", "сумма должна быть числом")
		case fee < 0:
			v.add("feeAmount", "сумма не может быть отрицательной")
		case in.PaymentMethod != PaymentMethodAcquiring:
			v.add("feeAmount", "комиссия эквайринга указывается только при оплате картой")
		}
	}

	v.optional("note", in.Note, 2000)

	if in.OccurredAt.IsZero() {
		v.add("occurredAt", "обязательное поле")
	}

	return v.err()
}

func transactionSummary(kind string) string {
	switch kind {
	case TransactionReceipt:
		return "Поступление"
	case TransactionRefund:
		return "Возврат"
	case TransactionOperatorTransfer:
		return "Перевод туроператору"
	case TransactionBonusIncome:
		return "Дополнительная выгода"
	default:
		return "Транзакция"
	}
}

const transactionColumns = `id, application_id, kind, amount::text, payer_id, tour_operator_id,
	payment_method, fee_amount::text, occurred_at, note, created_by, created_at, archived_at`

func scanTransaction(row interface{ Scan(...any) error }) (Transaction, error) {
	var t Transaction
	err := row.Scan(&t.ID, &t.ApplicationID, &t.Kind, &t.Amount, &t.PayerID, &t.TourOperatorID,
		&t.PaymentMethod, &t.FeeAmount, &t.OccurredAt, &t.Note, &t.CreatedBy, &t.CreatedAt, &t.ArchivedAt)
	if err != nil {
		return Transaction{}, mapError(err)
	}

	return t, nil
}

// CreateTransaction проводит транзакцию по заявке и записывает её в журнал
// изменений в той же транзакции базы данных. Если проведённое поступление
// закрывает полную стоимость заявки, автоматически завершаются её открытые
// дедлайны вида "payment" — единственная связь с application_deadlines:
// частичная оплата, возврат, перевод туроператору и доп. выгода дедлайны не
// трогают, а аннулирование поступления (VoidTransaction) их не переоткрывает.
func (s *Store) CreateTransaction(ctx context.Context, agencyID, applicationID string, actor Actor, input TransactionInput) (Transaction, error) {
	var created Transaction
	err := s.inTx(ctx, func(tx *Store) error {
		application, err := tx.Application(ctx, agencyID, applicationID)
		if err != nil {
			return err
		}

		if application.Currency != "RUB" {
			return &ValidationError{Fields: map[string]string{
				"currency": "финансовый учёт пока поддерживает только заявки в рублях",
			}}
		}

		const query = `
			INSERT INTO payment_transactions (
			    agency_id, application_id, kind, amount, payer_id, tour_operator_id,
			    payment_method, fee_amount, occurred_at, note, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING ` + transactionColumns

		transaction, err := scanTransaction(tx.db.QueryRow(ctx, query, agencyID, applicationID,
			input.Kind, input.Amount, input.PayerID, input.TourOperatorID,
			input.PaymentMethod, input.FeeAmount, input.OccurredAt, input.Note, nullString(actor.UserID)))
		if err != nil {
			return err
		}
		created = transaction

		if err := tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPaymentTransaction,
			EntityID:   transaction.ID,
			Action:     ActionCreate,
			Changes:    diff(TransactionInput{}, input),
			Summary:    transactionSummary(input.Kind) + " по заявке № " + application.Number,
		}); err != nil {
			return err
		}

		if input.Kind != TransactionReceipt {
			return nil
		}

		balance, err := tx.ApplicationBalance(ctx, agencyID, applicationID)
		if err != nil {
			return err
		}
		if balance.PaymentStatus != PaymentStatusPaid {
			return nil
		}

		const closeDeadlines = `
			UPDATE application_deadlines
			SET completed_at = now()
			WHERE agency_id = $1 AND application_id = $2 AND kind = 'payment' AND completed_at IS NULL`

		_, err = tx.db.Exec(ctx, closeDeadlines, agencyID, applicationID)

		return mapError(err)
	})

	return created, err
}

// Transaction загружает одну транзакцию внутри агентства.
func (s *Store) Transaction(ctx context.Context, agencyID, id string) (Transaction, error) {
	const query = `SELECT ` + transactionColumns + ` FROM payment_transactions WHERE agency_id = $1 AND id = $2`

	return scanTransaction(s.db.QueryRow(ctx, query, agencyID, id))
}

// ListApplicationTransactions возвращает журнал проводок одной заявки.
func (s *Store) ListApplicationTransactions(ctx context.Context, agencyID, applicationID string) ([]Transaction, error) {
	const query = `
		SELECT ` + transactionColumns + `
		FROM payment_transactions
		WHERE agency_id = $1 AND application_id = $2 AND archived_at IS NULL
		ORDER BY occurred_at DESC, created_at DESC`

	rows, err := s.db.Query(ctx, query, agencyID, applicationID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	transactions := make([]Transaction, 0, 8)
	for rows.Next() {
		transaction, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, transaction)
	}

	return transactions, mapError(rows.Err())
}

// TransactionFilter управляет общим журналом проводок агентства.
type TransactionFilter struct {
	Kind           string
	ApplicationID  string
	PayerID        string
	TourOperatorID string
	OccurredFrom   *Date
	OccurredTo     *Date
	Limit          int
	Offset         int
}

// ListTransactions возвращает проводки агентства, подходящие под фильтр —
// журнал для сверки и поиска «куда делась оплата».
func (s *Store) ListTransactions(ctx context.Context, agencyID string, filter TransactionFilter) ([]Transaction, int, error) {
	const query = `
		SELECT ` + transactionColumns + `, count(*) OVER () AS total
		FROM payment_transactions
		WHERE agency_id = $1
		  AND archived_at IS NULL
		  AND ($2 = '' OR kind = $2)
		  AND ($3 = '' OR application_id = $3::uuid)
		  AND ($4 = '' OR payer_id = $4::uuid)
		  AND ($5 = '' OR tour_operator_id = $5::uuid)
		  AND ($6::date IS NULL OR occurred_at >= $6)
		  AND ($7::date IS NULL OR occurred_at <= $7)
		ORDER BY occurred_at DESC, created_at DESC
		LIMIT $8 OFFSET $9`

	rows, err := s.db.Query(ctx, query, agencyID, filter.Kind, filter.ApplicationID, filter.PayerID,
		filter.TourOperatorID, filter.OccurredFrom, filter.OccurredTo, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	transactions := make([]Transaction, 0, filter.Limit)
	total := 0
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.ApplicationID, &t.Kind, &t.Amount, &t.PayerID, &t.TourOperatorID,
			&t.PaymentMethod, &t.FeeAmount, &t.OccurredAt, &t.Note, &t.CreatedBy, &t.CreatedAt, &t.ArchivedAt, &total); err != nil {
			return nil, 0, mapError(err)
		}
		transactions = append(transactions, t)
	}

	return transactions, total, mapError(rows.Err())
}

// VoidTransaction мягко аннулирует ошибочную проводку. Дедлайны, закрытые
// при её проведении, обратно не открываются — см. комментарий у CreateTransaction.
func (s *Store) VoidTransaction(ctx context.Context, agencyID, id string, actor Actor) error {
	return s.inTx(ctx, func(tx *Store) error {
		const query = `
			UPDATE payment_transactions SET archived_at = now()
			WHERE agency_id = $1 AND id = $2 AND archived_at IS NULL
			RETURNING application_id, kind`

		var applicationID, kind string
		if err := tx.db.QueryRow(ctx, query, agencyID, id).Scan(&applicationID, &kind); err != nil {
			return mapError(err)
		}

		application, err := tx.Application(ctx, agencyID, applicationID)
		if err != nil {
			return err
		}

		return tx.recordChange(ctx, agencyID, actor, changeRecord{
			EntityType: EntityPaymentTransaction,
			EntityID:   id,
			Action:     ActionArchive,
			Summary:    transactionSummary(kind) + " по заявке № " + application.Number,
		})
	})
}

// Balance — денежный итог по одной заявке.
type Balance struct {
	PriceTotal    *string `json:"priceTotal,omitempty"`
	Received      string  `json:"received"`
	Refunded      string  `json:"refunded"`
	NetReceived   string  `json:"netReceived"`
	Transferred   string  `json:"transferred"`
	BonusIncome   string  `json:"bonusIncome"`
	AcquiringFees string  `json:"acquiringFees"`
	AgencyIncome  string  `json:"agencyIncome"`
	PaymentStatus string  `json:"paymentStatus,omitempty"`
}

// ApplicationBalance считает баланс заявки поверх VIEW application_balances.
// Арифметика денег — в SQL над numeric, а не в Go над float64, чтобы копейки
// не терялись при округлении.
func (s *Store) ApplicationBalance(ctx context.Context, agencyID, applicationID string) (Balance, error) {
	const query = `
		SELECT
		    price_total::text,
		    received::text,
		    refunded::text,
		    (received - refunded)::text,
		    transferred::text,
		    bonus_income::text,
		    acquiring_fees::text,
		    (received - refunded - transferred)::text,
		    CASE
		        WHEN price_total IS NULL THEN ''
		        WHEN received - refunded <= 0 THEN 'unpaid'
		        WHEN received - refunded < price_total THEN 'partial'
		        WHEN received - refunded = price_total THEN 'paid'
		        ELSE 'overpaid'
		    END
		FROM application_balances
		WHERE agency_id = $1 AND application_id = $2`

	var balance Balance
	err := s.db.QueryRow(ctx, query, agencyID, applicationID).Scan(
		&balance.PriceTotal, &balance.Received, &balance.Refunded, &balance.NetReceived,
		&balance.Transferred, &balance.BonusIncome, &balance.AcquiringFees,
		&balance.AgencyIncome, &balance.PaymentStatus)
	if err != nil {
		return Balance{}, mapError(err)
	}

	return balance, nil
}

// ApplicationFinanceSummary — сокращённая денежная сводка для списка заявок
// (в отличие от Balance, который отдаёт /applications/{id}/finance). Те же
// формулы, что ApplicationBalance: Transferred — сумма operator_transfer,
// NetReceived — received минус refunded, AgencyIncome — NetReceived минус
// Transferred (деньги, которые агентство ещё не отдало туроператору,
// считаются его доходом).
type ApplicationFinanceSummary struct {
	Transferred  string `json:"transferred"`
	NetReceived  string `json:"netReceived"`
	AgencyIncome string `json:"agencyIncome"`
}

// PeriodRevenue — суммы одного периода в базовом финансовом отчёте.
type PeriodRevenue struct {
	Period       Date   `json:"period"`
	Receipts     string `json:"receipts"`
	Refunds      string `json:"refunds"`
	Transferred  string `json:"transferred"`
	BonusIncome  string `json:"bonusIncome"`
	AgencyIncome string `json:"agencyIncome"`
}

// RevenueByPeriod агрегирует движение денег агентства по месяцам, кварталам
// или годам за [from, to]. Оборот (receipts/refunds/transferred/bonusIncome)
// и AgencyIncome (receipts - refunds - transferred) группируются по дате
// самой транзакции — деньги, которые агентство ещё не перечислило
// туроператору, считаются его доходом того периода, в котором получены.
func (s *Store) RevenueByPeriod(ctx context.Context, agencyID, unit string, from, to Date) ([]PeriodRevenue, error) {
	v := newValidator()
	v.oneOf("unit", unit, "month", "quarter", "year")
	if err := v.err(); err != nil {
		return nil, err
	}

	const query = `
		SELECT
		    date_trunc($2, occurred_at)::date AS period,
		    COALESCE(SUM(amount) FILTER (WHERE kind = 'receipt'), 0)::text AS receipts,
		    COALESCE(SUM(amount) FILTER (WHERE kind = 'refund'), 0)::text AS refunds,
		    COALESCE(SUM(amount) FILTER (WHERE kind = 'operator_transfer'), 0)::text AS transferred,
		    COALESCE(SUM(amount) FILTER (WHERE kind = 'bonus_income'), 0)::text AS bonus_income,
		    (COALESCE(SUM(amount) FILTER (WHERE kind = 'receipt'), 0)
		        - COALESCE(SUM(amount) FILTER (WHERE kind = 'refund'), 0)
		        - COALESCE(SUM(amount) FILTER (WHERE kind = 'operator_transfer'), 0))::text AS agency_income
		FROM payment_transactions
		WHERE agency_id = $1 AND archived_at IS NULL
		  AND occurred_at BETWEEN $3 AND $4
		GROUP BY period
		ORDER BY period`

	rows, err := s.db.Query(ctx, query, agencyID, unit, from, to)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	periods := make([]PeriodRevenue, 0, 12)
	for rows.Next() {
		var p PeriodRevenue
		if err := rows.Scan(&p.Period, &p.Receipts, &p.Refunds, &p.Transferred,
			&p.BonusIncome, &p.AgencyIncome); err != nil {
			return nil, mapError(err)
		}
		periods = append(periods, p)
	}

	return periods, mapError(rows.Err())
}

-- Финансовый учёт: журнал фактов движения денег по заявке.
--
-- Агентское вознаграждение не хранится отдельной строкой — оно не факт
-- движения денег, а разница между стоимостью поездки и суммой, ушедшей
-- туроператору (price_total - SUM(operator_transfer)). Эта разница
-- считается на лету поверх application_balances ниже.

CREATE TABLE payment_transactions (
    id             uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    agency_id      uuid NOT NULL REFERENCES agencies (id) ON DELETE RESTRICT,
    application_id uuid NOT NULL,

    kind text NOT NULL CHECK (kind IN (
        'receipt', 'operator_transfer', 'refund', 'bonus_income'
    )),
    amount numeric(14, 2) NOT NULL CHECK (amount > 0),

    -- receipt/refund движутся через плательщика; operator_transfer/
    -- bonus_income — через туроператора. Направление задаёт kind, знак
    -- суммы не используется — как и везде в схеме, отрицательных сумм нет.
    payer_id         uuid,
    tour_operator_id uuid,

    payment_method text NOT NULL CHECK (payment_method IN ('cash', 'bank_transfer', 'card_acquiring')),
    fee_amount     numeric(14, 2) CHECK (fee_amount IS NULL OR fee_amount >= 0),

    -- Дата, когда деньги фактически перешли из рук в руки, — не дата ввода
    -- записи (та в created_at). Разбивка отчётов по периодам идёт по ней.
    occurred_at date NOT NULL DEFAULT current_date,

    note text,

    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,

    CONSTRAINT payment_transactions_identity_ck CHECK (
        (kind IN ('receipt', 'refund')
            AND payer_id IS NOT NULL AND tour_operator_id IS NULL)
        OR (kind IN ('operator_transfer', 'bonus_income')
            AND tour_operator_id IS NOT NULL AND payer_id IS NULL)
    ),
    CONSTRAINT payment_transactions_fee_ck CHECK (
        fee_amount IS NULL OR (payment_method = 'card_acquiring' AND fee_amount <= amount)
    ),

    -- RESTRICT, а не SET NULL/CASCADE как у большинства ссылок в схеме: это
    -- финансовый журнал, и ни архивация заявки/плательщика/туроператора, ни
    -- гипотетическое их удаление не должны молча стереть или обессмыслить
    -- факт движения денег (SET NULL здесь вдобавок сразу же нарушил бы
    -- payment_transactions_identity_ck).
    FOREIGN KEY (application_id, agency_id) REFERENCES applications (id, agency_id) ON DELETE RESTRICT,
    FOREIGN KEY (payer_id, agency_id) REFERENCES payers (id, agency_id) ON DELETE RESTRICT,
    FOREIGN KEY (tour_operator_id, agency_id) REFERENCES tour_operators (id, agency_id) ON DELETE RESTRICT
);

CREATE INDEX payment_transactions_application_idx
    ON payment_transactions (application_id, occurred_at) WHERE archived_at IS NULL;
CREATE INDEX payment_transactions_agency_idx
    ON payment_transactions (agency_id, occurred_at DESC) WHERE archived_at IS NULL;
CREATE INDEX payment_transactions_operator_idx
    ON payment_transactions (agency_id, tour_operator_id) WHERE tour_operator_id IS NOT NULL AND archived_at IS NULL;
CREATE INDEX payment_transactions_payer_idx
    ON payment_transactions (payer_id) WHERE payer_id IS NOT NULL;

-- Баланс заявки одним запросом: сколько получено, возвращено, перечислено
-- туроператору и сколько дополнительной выгоды получено сверху. Комиссия и
-- статус оплаты — производные величины поверх этих сумм, считаются в Go
-- (Store.ApplicationBalance), чтобы формула не разошлась в двух местах.
CREATE VIEW application_balances AS
SELECT
    a.id AS application_id,
    a.agency_id,
    a.price_total,
    COALESCE(SUM(t.amount) FILTER (WHERE t.kind = 'receipt'), 0)           AS received,
    COALESCE(SUM(t.amount) FILTER (WHERE t.kind = 'refund'), 0)            AS refunded,
    COALESCE(SUM(t.amount) FILTER (WHERE t.kind = 'operator_transfer'), 0) AS transferred,
    COALESCE(SUM(t.amount) FILTER (WHERE t.kind = 'bonus_income'), 0)      AS bonus_income,
    COALESCE(SUM(t.fee_amount), 0)                                        AS acquiring_fees
FROM applications a
LEFT JOIN payment_transactions t
    ON t.application_id = a.id AND t.agency_id = a.agency_id AND t.archived_at IS NULL
WHERE a.archived_at IS NULL
GROUP BY a.id, a.agency_id, a.price_total;

ALTER TABLE entity_changes DROP CONSTRAINT entity_changes_entity_type_check;
ALTER TABLE entity_changes ADD CONSTRAINT entity_changes_entity_type_check CHECK (entity_type IN (
    'tourist', 'application', 'payer', 'partner', 'tour_operator',
    'acquisition_channel', 'user', 'session', 'payment_transaction'
));

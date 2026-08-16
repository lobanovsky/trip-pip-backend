package store

import "context"

// StatusCount — число заявок в одном статусе.
type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// ApplicationFunnel — статусная сводка и показатели эффективности агентства
// за период [from, to] среди заявок, созданных в этом периоде (created_at).
// ConversionRate и AverageCheck — nil, если для них нет данных (например, в
// периоде нет ни одной completed/cancelled заявки).
type ApplicationFunnel struct {
	StatusCounts   []StatusCount `json:"statusCounts"`
	Total          int           `json:"total"`
	Completed      int           `json:"completed"`
	Cancelled      int           `json:"cancelled"`
	ConversionRate *float64      `json:"conversionRate,omitempty"`
	AverageCheck   *string       `json:"averageCheck,omitempty"`
}

// ApplicationFunnel считает статусную воронку и эффективность агентства
// среди заявок, созданных в [from, to]. Конверсия и средний чек — производные
// величины над тем же множеством заявок, поэтому считаются одним запросом:
// раздельные вызовы дали бы риск несогласованных данных при разных from/to.
func (s *Store) ApplicationFunnel(ctx context.Context, agencyID string, from, to Date) (ApplicationFunnel, error) {
	const query = `
		SELECT
		    count(*) FILTER (WHERE status = 'inquiry'),
		    count(*) FILTER (WHERE status = 'selection'),
		    count(*) FILTER (WHERE status = 'approval'),
		    count(*) FILTER (WHERE status = 'booked'),
		    count(*) FILTER (WHERE status = 'preparation'),
		    count(*) FILTER (WHERE status = 'completed'),
		    count(*) FILTER (WHERE status = 'cancelled'),
		    count(*),
		    CASE WHEN count(*) FILTER (WHERE status = 'completed') = 0 THEN NULL
		         ELSE AVG(price_total) FILTER (WHERE status = 'completed')::numeric(14,2)::text
		    END
		FROM applications
		WHERE agency_id = $1 AND archived_at IS NULL AND created_at BETWEEN $2 AND $3`

	var inquiry, selection, approval, booked, preparation, completed, cancelled, total int
	var avgCheck *string
	err := s.db.QueryRow(ctx, query, agencyID, from, to).Scan(
		&inquiry, &selection, &approval, &booked, &preparation, &completed, &cancelled, &total, &avgCheck)
	if err != nil {
		return ApplicationFunnel{}, mapError(err)
	}

	funnel := ApplicationFunnel{
		StatusCounts: []StatusCount{
			{StatusInquiry, inquiry}, {StatusSelection, selection}, {StatusApproval, approval},
			{StatusBooked, booked}, {StatusPreparation, preparation},
			{StatusCompleted, completed}, {StatusCancelled, cancelled},
		},
		Total: total, Completed: completed, Cancelled: cancelled, AverageCheck: avgCheck,
	}
	if denom := completed + cancelled; denom > 0 {
		rate := float64(completed) / float64(denom)
		funnel.ConversionRate = &rate
	}

	return funnel, nil
}

// DirectionStat — одна строка топа направлений: страна, число заявок и
// сумма price_total среди заявок, созданных в периоде.
type DirectionStat struct {
	Country      string `json:"country"`
	Applications int    `json:"applications"`
	PriceTotal   string `json:"priceTotal"`
}

// DirectionStats возвращает топ направлений по числу заявок, созданных в
// [from, to]. Заявки без указанной страны не участвуют — направление ещё не
// определено на ранних стадиях воронки (inquiry/selection).
func (s *Store) DirectionStats(ctx context.Context, agencyID string, from, to Date, limit int) ([]DirectionStat, error) {
	const query = `
		SELECT country, count(*)::int AS applications, COALESCE(SUM(price_total), 0)::text AS price_total
		FROM applications
		WHERE agency_id = $1 AND archived_at IS NULL AND created_at BETWEEN $2 AND $3
		  AND country IS NOT NULL AND country <> ''
		GROUP BY country
		ORDER BY count(*) DESC, SUM(price_total) DESC
		LIMIT $4`

	rows, err := s.db.Query(ctx, query, agencyID, from, to, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	stats := make([]DirectionStat, 0, limit)
	for rows.Next() {
		var d DirectionStat
		if err := rows.Scan(&d.Country, &d.Applications, &d.PriceTotal); err != nil {
			return nil, mapError(err)
		}
		stats = append(stats, d)
	}

	return stats, mapError(rows.Err())
}

// OperatorStat — одна строка топа туроператоров за период.
type OperatorStat struct {
	TourOperatorID string `json:"tourOperatorId"`
	Name           string `json:"name"`
	Applications   int    `json:"applications"`
	PriceTotal     string `json:"priceTotal"`
}

// OperatorStats возвращает топ туроператоров по числу заявок, созданных в
// [from, to]. Архивные операторы не исключаются: заявки, оформленные через
// оператора до его архивации, должны остаться в отчёте.
func (s *Store) OperatorStats(ctx context.Context, agencyID string, from, to Date, limit int) ([]OperatorStat, error) {
	const query = `
		SELECT a.tour_operator_id, o.name,
		       count(*)::int AS applications, COALESCE(SUM(a.price_total), 0)::text AS price_total
		FROM applications a
		JOIN tour_operators o ON o.id = a.tour_operator_id AND o.agency_id = a.agency_id
		WHERE a.agency_id = $1 AND a.archived_at IS NULL AND a.created_at BETWEEN $2 AND $3
		GROUP BY a.tour_operator_id, o.name
		ORDER BY count(*) DESC, SUM(a.price_total) DESC
		LIMIT $4`

	rows, err := s.db.Query(ctx, query, agencyID, from, to, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	stats := make([]OperatorStat, 0, limit)
	for rows.Next() {
		var o OperatorStat
		if err := rows.Scan(&o.TourOperatorID, &o.Name, &o.Applications, &o.PriceTotal); err != nil {
			return nil, mapError(err)
		}
		stats = append(stats, o)
	}

	return stats, mapError(rows.Err())
}

// ChannelStat — одна строка отчёта по источникам клиентов за период: сколько
// новых туристов привёл канал и сколько заявок/выручки он принёс.
type ChannelStat struct {
	ChannelID    string `json:"channelId"`
	Name         string `json:"name"`
	NewTourists  int    `json:"newTourists"`
	Applications int    `json:"applications"`
	PriceTotal   string `json:"priceTotal"`
}

// ChannelStats — эффективность каналов привлечения за [from, to]: новые
// туристы считаются по tourists.created_at, заявки и выручка — по
// applications.created_at, оба разреза по acquisition_channel_id.
func (s *Store) ChannelStats(ctx context.Context, agencyID string, from, to Date, limit int) ([]ChannelStat, error) {
	const query = `
		WITH app_stats AS (
		    SELECT acquisition_channel_id AS channel_id,
		           count(*)::int AS applications,
		           COALESCE(SUM(price_total), 0) AS price_total
		    FROM applications
		    WHERE agency_id = $1 AND archived_at IS NULL AND created_at BETWEEN $2 AND $3
		      AND acquisition_channel_id IS NOT NULL
		    GROUP BY acquisition_channel_id
		),
		tourist_stats AS (
		    SELECT acquisition_channel_id AS channel_id, count(*)::int AS new_tourists
		    FROM tourists
		    WHERE agency_id = $1 AND archived_at IS NULL AND created_at BETWEEN $2 AND $3
		      AND acquisition_channel_id IS NOT NULL
		    GROUP BY acquisition_channel_id
		)
		SELECT c.id, c.name,
		       COALESCE(t.new_tourists, 0),
		       COALESCE(a.applications, 0),
		       COALESCE(a.price_total, 0)::text
		FROM acquisition_channels c
		LEFT JOIN app_stats a ON a.channel_id = c.id
		LEFT JOIN tourist_stats t ON t.channel_id = c.id
		WHERE c.agency_id = $1 AND (a.applications IS NOT NULL OR t.new_tourists IS NOT NULL)
		ORDER BY COALESCE(a.applications, 0) DESC, COALESCE(t.new_tourists, 0) DESC
		LIMIT $4`

	rows, err := s.db.Query(ctx, query, agencyID, from, to, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	stats := make([]ChannelStat, 0, limit)
	for rows.Next() {
		var c ChannelStat
		if err := rows.Scan(&c.ChannelID, &c.Name, &c.NewTourists, &c.Applications, &c.PriceTotal); err != nil {
			return nil, mapError(err)
		}
		stats = append(stats, c)
	}

	return stats, mapError(rows.Err())
}

// RepeatCustomer — заказчик с 2+ неотменёнными заявками, попавший в топ
// повторных клиентов отчёта.
type RepeatCustomer struct {
	TouristID    string `json:"touristId"`
	FullName     string `json:"fullName"`
	Applications int    `json:"applications"`
}

// RepeatCustomersReport — доля повторных клиентов среди заказчиков,
// оформивших хотя бы одну заявку в [from, to].
type RepeatCustomersReport struct {
	TotalCustomers     int              `json:"totalCustomers"`
	RepeatCustomers    int              `json:"repeatCustomers"`
	RepeatShare        *float64         `json:"repeatShare,omitempty"`
	TopRepeatCustomers []RepeatCustomer `json:"topRepeatCustomers"`
}

// RepeatCustomerReport считает долю повторных клиентов. «Повторный» —
// заказчик с 2+ неотменёнными заявками за всё время (независимо от того,
// когда они созданы); «за период» — среди заказчиков, у которых есть хотя бы
// одна заявка (любого статуса), созданная в [from, to]. Знаменатель и
// числитель поэтому считаются над разными временными окнами по построению.
func (s *Store) RepeatCustomerReport(ctx context.Context, agencyID string, from, to Date, limit int) (RepeatCustomersReport, error) {
	const summaryQuery = `
		WITH period_customers AS (
		    SELECT DISTINCT customer_tourist_id
		    FROM applications
		    WHERE agency_id = $1 AND archived_at IS NULL AND created_at BETWEEN $2 AND $3
		),
		customer_totals AS (
		    SELECT customer_tourist_id, count(*) FILTER (WHERE status <> 'cancelled') AS active_count
		    FROM applications
		    WHERE agency_id = $1 AND archived_at IS NULL
		    GROUP BY customer_tourist_id
		)
		SELECT count(*), count(*) FILTER (WHERE ct.active_count >= 2)
		FROM period_customers pc
		JOIN customer_totals ct ON ct.customer_tourist_id = pc.customer_tourist_id`

	var report RepeatCustomersReport
	if err := s.db.QueryRow(ctx, summaryQuery, agencyID, from, to).
		Scan(&report.TotalCustomers, &report.RepeatCustomers); err != nil {
		return RepeatCustomersReport{}, mapError(err)
	}
	if report.TotalCustomers > 0 {
		share := float64(report.RepeatCustomers) / float64(report.TotalCustomers)
		report.RepeatShare = &share
	}

	const topQuery = `
		WITH period_customers AS (
		    SELECT DISTINCT customer_tourist_id
		    FROM applications
		    WHERE agency_id = $1 AND archived_at IS NULL AND created_at BETWEEN $2 AND $3
		),
		customer_totals AS (
		    SELECT customer_tourist_id, count(*) FILTER (WHERE status <> 'cancelled') AS active_count
		    FROM applications
		    WHERE agency_id = $1 AND archived_at IS NULL
		    GROUP BY customer_tourist_id
		)
		SELECT t.id, t.last_name || ' ' || t.first_name, ct.active_count
		FROM period_customers pc
		JOIN customer_totals ct ON ct.customer_tourist_id = pc.customer_tourist_id
		JOIN tourists t ON t.id = pc.customer_tourist_id
		WHERE ct.active_count >= 2
		ORDER BY ct.active_count DESC, t.last_name
		LIMIT $4`

	rows, err := s.db.Query(ctx, topQuery, agencyID, from, to, limit)
	if err != nil {
		return RepeatCustomersReport{}, mapError(err)
	}
	defer rows.Close()

	report.TopRepeatCustomers = make([]RepeatCustomer, 0, limit)
	for rows.Next() {
		var c RepeatCustomer
		if err := rows.Scan(&c.TouristID, &c.FullName, &c.Applications); err != nil {
			return RepeatCustomersReport{}, mapError(err)
		}
		report.TopRepeatCustomers = append(report.TopRepeatCustomers, c)
	}

	return report, mapError(rows.Err())
}

-- Индексы под отчёты аналитики (GET /api/reports/*): все запросы начинаются
-- с одного и того же предиката agency_id + archived_at IS NULL + диапазон
-- created_at, прежде чем сгруппироваться по статусу/стране/оператору/каналу/
-- заказчику.
CREATE INDEX applications_created_idx ON applications (agency_id, created_at) WHERE archived_at IS NULL;
CREATE INDEX tourists_created_idx ON tourists (agency_id, created_at) WHERE archived_at IS NULL;

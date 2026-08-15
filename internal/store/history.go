package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Типы сущностей, записываемые в журнал изменений.
const (
	EntityTourist            = "tourist"
	EntityApplication        = "application"
	EntityPayer              = "payer"
	EntityPartner            = "partner"
	EntityTourOperator       = "tour_operator"
	EntityAcquisitionChannel = "acquisition_channel"
	EntityUser               = "user"
	EntitySession            = "session"
	EntityPaymentTransaction = "payment_transaction"
)

// Действия, записываемые в журнал изменений.
const (
	ActionCreate       = "create"
	ActionUpdate       = "update"
	ActionArchive      = "archive"
	ActionRestore      = "restore"
	ActionStatusChange = "status_change"
	ActionLogin        = "login"
	ActionLogout       = "logout"
	ActionLoginFailed  = "login_failed"
)

// maskedValue заменяет персональные данные в журнале. Сам факт изменения
// номера паспорта фиксируется; значение номера — нет.
const maskedValue = "***"

// Change — значение одного поля до и после изменения.
type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// ChangeEntry — одна запись журнала.
type ChangeEntry struct {
	ID         int64             `json:"id"`
	EntityType string            `json:"entityType"`
	EntityID   string            `json:"entityId"`
	Action     string            `json:"action"`
	Changes    map[string]Change `json:"changes"`
	Summary    string            `json:"summary,omitempty"`
	ActorID    *string           `json:"actorId,omitempty"`
	ActorLabel string            `json:"actorLabel"`
	CreatedAt  time.Time         `json:"createdAt"`
}

// Actor определяет, кто совершил изменение. Нулевой Actor означает системное
// действие — так поступают bootstrap и уборщик сессий.
type Actor struct {
	UserID    string
	Label     string
	RequestID string
}

type changeRecord struct {
	EntityType string
	EntityID   string
	Action     string
	Changes    map[string]Change
	Summary    string
}

func (s *Store) recordChange(ctx context.Context, agencyID string, actor Actor, record changeRecord) error {
	const query = `
		INSERT INTO entity_changes
		    (agency_id, entity_type, entity_id, action, changes, summary, actor_id, actor_label, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	changes := record.Changes
	if changes == nil {
		changes = map[string]Change{}
	}

	var actorID *string
	if actor.UserID != "" {
		actorID = &actor.UserID
	}

	_, err := s.db.Exec(ctx, query,
		agencyID, record.EntityType, record.EntityID, record.Action,
		changes, nullString(record.Summary), actorID, actor.Label, nullString(actor.RequestID))
	if err != nil {
		return fmt.Errorf("record change: %w", mapError(err))
	}

	return nil
}

// RecordAccess журналирует событие без диффа полей, например вход в
// систему. Это тот самый журнал доступа, которого требует 152-ФЗ, и стоит
// он одну вставку.
func (s *Store) RecordAccess(ctx context.Context, agencyID string, actor Actor, entityType, entityID, action, summary string) error {
	return s.recordChange(ctx, agencyID, actor, changeRecord{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     action,
		Summary:    summary,
	})
}

// ListHistory возвращает журнал по одной записи, сначала новые.
func (s *Store) ListHistory(ctx context.Context, agencyID, entityType, entityID string, limit, offset int) ([]ChangeEntry, int, error) {
	const query = `
		SELECT id, entity_type, entity_id, action, changes, coalesce(summary, ''),
		       actor_id, actor_label, created_at, count(*) OVER () AS total
		FROM entity_changes
		WHERE agency_id = $1 AND entity_type = $2 AND entity_id = $3
		ORDER BY created_at DESC, id DESC
		LIMIT $4 OFFSET $5`

	rows, err := s.db.Query(ctx, query, agencyID, entityType, entityID, limit, offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	entries := make([]ChangeEntry, 0, limit)
	total := 0
	for rows.Next() {
		var entry ChangeEntry
		if err := rows.Scan(&entry.ID, &entry.EntityType, &entry.EntityID, &entry.Action,
			&entry.Changes, &entry.Summary, &entry.ActorID, &entry.ActorLabel, &entry.CreatedAt, &total); err != nil {
			return nil, 0, mapError(err)
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, mapError(err)
	}

	return entries, total, nil
}

// diff сравнивает два значения одного и того же типа структуры и сообщает,
// какие поля изменились, используя в качестве ключа имя из тега `history`.
//
// Поле с тегом `history:"-"` игнорируется. Поле с опцией "sensitive"
// фиксирует сам факт изменения без записи значений — так номера паспортов не
// попадают в журнал, оставаясь при этом аудируемыми.
func diff(before, after any) map[string]Change {
	beforeValue := reflect.ValueOf(before)
	afterValue := reflect.ValueOf(after)

	if beforeValue.Kind() != reflect.Struct || afterValue.Type() != beforeValue.Type() {
		return map[string]Change{}
	}

	changes := make(map[string]Change)
	fields := beforeValue.Type()

	for i := range fields.NumField() {
		field := fields.Field(i)
		if !field.IsExported() {
			continue
		}

		name, sensitive, ok := historyTag(field.Tag.Get("history"))
		if !ok {
			continue
		}

		oldValue := beforeValue.Field(i).Interface()
		newValue := afterValue.Field(i).Interface()
		if equalValues(oldValue, newValue) {
			continue
		}

		if sensitive {
			changes[name] = Change{From: maskedValue, To: maskedValue}

			continue
		}

		changes[name] = Change{From: derefValue(oldValue), To: derefValue(newValue)}
	}

	return changes
}

func historyTag(tag string) (name string, sensitive, ok bool) {
	if tag == "" || tag == "-" {
		return "", false, false
	}

	name, rest, _ := strings.Cut(tag, ",")
	if name == "" {
		return "", false, false
	}

	return name, rest == "sensitive", true
}

// equalValues сравнивает через указатели, поэтому изменение *string
// оценивается по тексту, на который он указывает, а не по адресу.
func equalValues(a, b any) bool {
	return reflect.DeepEqual(derefValue(a), derefValue(b))
}

func derefValue(value any) any {
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Ptr {
		return value
	}
	if reflected.IsNil() {
		return nil
	}

	return reflected.Elem().Interface()
}

func nullString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

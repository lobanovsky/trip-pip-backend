package store

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound покрывает и строки, которые существуют в другом агентстве:
	// вызывающей стороне не положено узнавать разницу.
	ErrNotFound = errors.New("not found")

	// ErrConflict — нарушение уникальности: дубль туриста, уже
	// зарегистрированный email, повторно использованное в одном агентстве
	// название туроператора.
	ErrConflict = errors.New("conflict")

	// ErrInvalidReference — нарушение внешнего ключа. В мультитенантной схеме
	// так же выглядит и попытка связать записи разных агентств, потому что
	// составные ключи делают такую строку невозможной.
	ErrInvalidReference = errors.New("invalid reference")

	// ErrInvalidValue — CHECK-ограничение, которое запрос проскочил мимо
	// валидации на уровне API.
	ErrInvalidValue = errors.New("invalid value")

	// ErrVersionConflict означает, что кто-то другой уже сохранил запись раньше.
	ErrVersionConflict = errors.New("version conflict")
)

// ConstraintError называет ограничение базы данных, отклонившее запись,
// поэтому обработчики могут превратить "tourists_passport_uk" в сообщение
// про дублирующийся паспорт, а не в общий конфликт.
type ConstraintError struct {
	Kind       error
	Constraint string
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Constraint)
}

func (e *ConstraintError) Unwrap() error { return e.Kind }

// ConstraintName возвращает имя нарушенного ограничения или "", если err —
// не ошибка ограничения.
func ConstraintName(err error) string {
	var constraintErr *ConstraintError
	if errors.As(err, &constraintErr) {
		return constraintErr.Constraint
	}

	return ""
}

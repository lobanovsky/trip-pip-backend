package store

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// dateLayout — формат календарной даты для передачи и хранения.
const dateLayout = "2006-01-02"

// Date — календарная дата без времени и без часового пояса: день рождения,
// дата выдачи паспорта, дата вылета. Это то, что человек пишет в форме,
// поэтому значение не должно сдвигаться из-за того, что сервер или клиент
// находятся в другом часовом поясе.
//
// Реализует интерфейсы database/sql, которые pgx использует для колонок типа
// date, и JSON в виде "2006-01-02".
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// NewDate строит Date из time.Time, отбрасывая время суток и часовой пояс.
func NewDate(t time.Time) Date {
	year, month, day := t.Date()

	return Date{Year: year, Month: month, Day: day}
}

// ParseDate читает значение в формате "2006-01-02".
func ParseDate(value string) (Date, error) {
	parsed, err := time.Parse(dateLayout, value)
	if err != nil {
		return Date{}, fmt.Errorf("дата должна быть в формате ГГГГ-ММ-ДД")
	}

	return NewDate(parsed), nil
}

// Time возвращает полночь этой даты в указанном часовом поясе.
func (d Date) Time(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// IsZero сообщает, была ли дата когда-либо задана.
func (d Date) IsZero() bool { return d.Year == 0 && d.Month == 0 && d.Day == 0 }

func (d Date) String() string { return d.Time(time.UTC).Format(dateLayout) }

// Before сообщает, наступает ли d раньше other.
func (d Date) Before(other Date) bool {
	return d.Time(time.UTC).Before(other.Time(time.UTC))
}

// AddYears возвращает то же число месяца на заданное количество лет позже —
// именно так выражаются срок действия документа и возрастные пороги.
func (d Date) AddYears(years int) Date {
	return NewDate(d.Time(time.UTC).AddDate(years, 0, 0))
}

// AddDays сдвигает дату на указанное число дней.
func (d Date) AddDays(days int) Date {
	return NewDate(d.Time(time.UTC).AddDate(0, 0, days))
}

// DaysUntil считает целые дни от other до d; отрицательное значение, если d
// в прошлом.
func (d Date) DaysUntil(other Date) int {
	return int(d.Time(time.UTC).Sub(other.Time(time.UTC)).Hours() / 24)
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("дата должна быть строкой в формате ГГГГ-ММ-ДД")
	}

	if strings.TrimSpace(raw) == "" {
		*d = Date{}

		return nil
	}

	parsed, err := ParseDate(raw)
	if err != nil {
		return err
	}
	*d = parsed

	return nil
}

// Value реализует driver.Valuer, чтобы pgx мог отправить дату в PostgreSQL.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}

	return d.Time(time.UTC), nil
}

// Scan реализует sql.Scanner — его pgx использует для колонок типа date.
func (d *Date) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*d = Date{}

		return nil
	case time.Time:
		*d = NewDate(value)

		return nil
	case string:
		parsed, err := ParseDate(value)
		if err != nil {
			return err
		}
		*d = parsed

		return nil
	default:
		return fmt.Errorf("cannot scan %T into Date", src)
	}
}

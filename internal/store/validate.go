package store

import (
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// ValidationError собирает все проблемы с телом запроса, чтобы клиент мог
// исправить всю форму за один раз, а не по одному полю за попытку.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	return fmt.Sprintf("validation failed: %s", strings.Join(names, ", "))
}

type validator struct {
	fields map[string]string
}

func newValidator() *validator {
	return &validator{fields: make(map[string]string)}
}

func (v *validator) add(field, message string) {
	if _, exists := v.fields[field]; !exists {
		v.fields[field] = message
	}
}

func (v *validator) err() error {
	if len(v.fields) == 0 {
		return nil
	}

	return &ValidationError{Fields: v.fields}
}

// required проверяет обязательное текстовое поле.
func (v *validator) required(field, value string, maxLen int) {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		v.add(field, "обязательное поле")
	case len([]rune(trimmed)) > maxLen:
		v.add(field, fmt.Sprintf("не длиннее %d символов", maxLen))
	case hasControlChars(trimmed):
		v.add(field, "содержит недопустимые символы")
	}
}

// optional проверяет текстовое поле, которое может отсутствовать.
func (v *validator) optional(field string, value *string, maxLen int) {
	if value == nil {
		return
	}
	if len([]rune(*value)) > maxLen {
		v.add(field, fmt.Sprintf("не длиннее %d символов", maxLen))
	}
}

// pattern проверяет необязательное поле на соответствие фиксированному формату.
func (v *validator) pattern(field string, value *string, re *regexp.Regexp, message string) {
	if value == nil || *value == "" {
		return
	}
	if !re.MatchString(*value) {
		v.add(field, message)
	}
}

func (v *validator) oneOf(field, value string, allowed ...string) {
	for _, candidate := range allowed {
		if value == candidate {
			return
		}
	}
	v.add(field, "недопустимое значение: "+strings.Join(allowed, ", "))
}

func (v *validator) email(field string, value *string) {
	if value == nil || *value == "" {
		return
	}
	if _, err := mail.ParseAddress(*value); err != nil {
		v.add(field, "некорректный адрес электронной почты")
	}
}

func hasControlChars(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}

	return false
}

// Форматы, используемые и на уровне API, и в CHECK-ограничениях схемы.
// Если держать их здесь, отклонённое значение даёт понятный 400, а не 500
// из-за нарушения ограничения в базе данных.
var (
	passportSeriesRe = regexp.MustCompile(`^[0-9]{4}$`)
	passportNumberRe = regexp.MustCompile(`^[0-9]{6}$`)
	divisionCodeRe   = regexp.MustCompile(`^[0-9]{3}-[0-9]{3}$`)
	intlPassportRe   = regexp.MustCompile(`^[0-9]{9}$`)
	latinNameRe      = regexp.MustCompile(`^[A-Z][A-Z '-]*$`)
	innRe            = regexp.MustCompile(`^[0-9]{10}([0-9]{2})?$`)
	kppRe            = regexp.MustCompile(`^[0-9]{9}$`)
	ogrnRe           = regexp.MustCompile(`^[0-9]{13}([0-9]{2})?$`)
	currencyRe       = regexp.MustCompile(`^[A-Z]{3}$`)
	countryCodeRe    = regexp.MustCompile(`^[A-Z]{2}$`)
	channelCodeRe    = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	phoneDigitsRe    = regexp.MustCompile(`[^0-9+]`)
)

// NormalizePhone приводит номер телефона к сравнимой форме. Российские
// номера в формате 8XXXXXXXXXX превращаются в +7XXXXXXXXXX, поэтому один и
// тот же человек, введённый дважды, узнаваемо остаётся одним и тем же
// человеком; всё остальное сохраняет свои цифры и ведущий плюс, потому что
// агентства продают поездки и держателям иностранных паспортов.
func NormalizePhone(value string) string {
	cleaned := phoneDigitsRe.ReplaceAllString(value, "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}

	digits := strings.TrimPrefix(cleaned, "+")
	switch {
	case len(digits) == 11 && strings.HasPrefix(digits, "8"):
		return "+7" + digits[1:]
	case len(digits) == 11 && strings.HasPrefix(digits, "7"):
		return "+" + digits
	case len(digits) == 10 && !strings.HasPrefix(cleaned, "+"):
		return "+7" + digits
	default:
		return "+" + digits
	}
}

func (v *validator) phone(field string, value *string) {
	if value == nil || *value == "" {
		return
	}

	digits := strings.TrimPrefix(*value, "+")
	for _, r := range digits {
		if r < '0' || r > '9' {
			v.add(field, "телефон должен состоять из цифр")

			return
		}
	}
	if len(digits) < 8 || len(digits) > 15 {
		v.add(field, "телефон должен содержать от 8 до 15 цифр")
	}
}

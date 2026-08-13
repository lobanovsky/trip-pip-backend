// Package auth хеширует пароли и выпускает токены сессий.
//
// Пакет намеренно не имеет доступа к базе данных: store сохраняет то, что
// производит этот пакет, и ни один из них не заглядывает во внутренности
// другого.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Параметры argon2id по рекомендации OWASP о хранении паролей.
// Они записываются в каждый хеш, поэтому повышение параметров в будущем
// означает лишь то, что старые хеши обновятся при следующем входе владельца.
const (
	argonMemory  uint32 = 19 * 1024 // KiB
	argonTime    uint32 = 2
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// ErrInvalidHash означает, что сохранённое значение — не хеш, записанный этим пакетом.
var ErrInvalidHash = errors.New("invalid password hash")

// MinPasswordLength — минимальная допустимая длина пароля. Учётные записи
// сотрудников защищают паспортные данные, поэтому порог намеренно выше
// обычных восьми символов.
const MinPasswordLength = 12

// HashPassword вычисляет хеш для хранения. Результат самоописывающийся:
// $argon2id$v=19$m=19456,t=2,p=1$<соль>$<ключ>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword сообщает, совпадает ли пароль с закодированным хешем, и
// нужно ли перехешировать значение с текущими параметрами.
//
// Сравнение выполняется за постоянное время, поэтому вызывающая сторона не
// может по времени ответа узнать верный префикс хеша.
func VerifyPassword(encoded, password string) (ok bool, needsRehash bool, err error) {
	params, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}

	candidate := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(key)))
	if subtle.ConstantTimeCompare(key, candidate) != 1 {
		return false, false, nil
	}

	stale := params.memory != argonMemory || params.time != argonTime ||
		params.threads != argonThreads || uint32(len(key)) != argonKeyLen

	return true, stale, nil
}

type hashParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (hashParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return hashParams{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return hashParams{}, nil, nil, ErrInvalidHash
	}

	var params hashParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return hashParams{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return hashParams{}, nil, nil, ErrInvalidHash
	}

	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return hashParams{}, nil, nil, ErrInvalidHash
	}

	return params, salt, key, nil
}

// dummyHash проверяется, когда ни один аккаунт не соответствует
// отправленному email.
//
// Без этого отсутствующий аккаунт отвечал бы заметно быстрее, чем неверный
// пароль, а это превращает форму входа в способ перечислить, кто здесь
// работает.
var dummyHash = mustHash("trip-pip-dummy-password-value")

// VerifyDummy выполняет ту же работу, что и настоящая проверка, а затем
// сообщает о неудаче.
func VerifyDummy(password string) {
	_, _, _ = VerifyPassword(dummyHash, password)
}

func mustHash(password string) string {
	hash, err := HashPassword(password)
	if err != nil {
		panic("auth: cannot hash the dummy password: " + err.Error())
	}

	return hash
}

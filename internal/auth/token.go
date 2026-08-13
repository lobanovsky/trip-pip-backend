package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

// sessionTokenBytes даёт 256 бит энтропии, из-за чего подобрать действующую
// сессию практически невозможно.
const sessionTokenBytes = 32

// NewSessionToken возвращает новый непрозрачный токен. Хранится только его
// хеш, поэтому само значение существует в одном месте — в cookie клиента.
func NewSessionToken() string {
	buf := make([]byte, sessionTokenBytes)
	// crypto/rand.Read никогда не возвращает ошибку; вместо этого при сбое
	// программа паникует.
	_, _ = rand.Read(buf)

	return base64.RawURLEncoding.EncodeToString(buf)
}

// HashToken вычисляет значение, которое хранится в таблице sessions.
// SHA-256 здесь уместен там, где медленный хеш был бы лишним: вход уже
// является 256 случайными битами, поэтому подбирать по словарю нечего, а
// эта функция выполняется на каждом запросе.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))

	return sum[:]
}

// RateLimiter ограничивает количество повторных неудачных попыток по одному ключу.
//
// Это счётчик с фиксированным окном, хранящийся в памяти: развёртывание —
// одна реплика с файловой системой только для чтения, писать состояние
// некуда, а сброс счётчиков при передеплое — приемлемая плата за отсутствие
// внешней зависимости. Скользящие окна и разделяемое состояние понадобятся,
// когда появится вторая реплика.
type RateLimiter struct {
	mu      sync.Mutex
	hits    map[string]*window
	limit   int
	window  time.Duration
	nowFunc func() time.Time
}

type window struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter разрешает limit попыток на ключ в пределах окна.
func NewRateLimiter(limit int, per time.Duration) *RateLimiter {
	return &RateLimiter{
		hits:    make(map[string]*window),
		limit:   limit,
		window:  per,
		nowFunc: time.Now,
	}
}

// Allow фиксирует попытку и сообщает, не превышен ли лимит.
func (l *RateLimiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.nowFunc()
	current, ok := l.hits[key]
	if !ok || now.After(current.resetAt) {
		l.hits[key] = &window{count: 1, resetAt: now.Add(l.window)}
		l.sweep(now)

		return true
	}

	current.count++

	return current.count <= l.limit
}

// Reset очищает счётчик для ключа; вызывается после успешного входа.
func (l *RateLimiter) Reset(key string) {
	if l == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.hits, key)
}

// sweep убирает истёкшие окна, чтобы поток разных ключей не разрастил карту
// без ограничений. Выполняется под блокировкой вызывающей стороны.
func (l *RateLimiter) sweep(now time.Time) {
	if len(l.hits) < 1024 {
		return
	}

	for key, entry := range l.hits {
		if now.After(entry.resetAt) {
			delete(l.hits, key)
		}
	}
}

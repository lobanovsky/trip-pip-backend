package auth

import (
	"testing"
	"time"
)

func TestNewSessionTokenIsUnique(t *testing.T) {
	t.Parallel()

	first := NewSessionToken()
	second := NewSessionToken()

	if first == second {
		t.Error("NewSessionToken() returned the same value twice")
	}
	if len(first) == 0 {
		t.Error("NewSessionToken() returned an empty token")
	}
}

func TestHashTokenIsDeterministic(t *testing.T) {
	t.Parallel()

	token := "some-token-value"
	if string(HashToken(token)) != string(HashToken(token)) {
		t.Error("HashToken() is not deterministic")
	}
	if string(HashToken(token)) == string(HashToken(token+"x")) {
		t.Error("HashToken() collided on distinct inputs")
	}
}

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(3, time.Minute)

	for i := range 3 {
		if !limiter.Allow("key") {
			t.Fatalf("Allow() attempt %d = false, want true", i+1)
		}
	}

	if limiter.Allow("key") {
		t.Error("Allow() after the limit was reached = true, want false")
	}

	// У другого ключа — свой собственный лимит.
	if !limiter.Allow("other-key") {
		t.Error("Allow() for a distinct key = false, want true")
	}
}

func TestRateLimiterResetsWindow(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(1, time.Minute)
	now := time.Now()
	limiter.nowFunc = func() time.Time { return now }

	if !limiter.Allow("key") {
		t.Fatal("Allow() first attempt = false, want true")
	}
	if limiter.Allow("key") {
		t.Fatal("Allow() second attempt within the window = true, want false")
	}

	now = now.Add(2 * time.Minute)
	if !limiter.Allow("key") {
		t.Error("Allow() after the window elapsed = false, want true")
	}
}

func TestRateLimiterResetClearsCounter(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(1, time.Minute)

	limiter.Allow("key")
	limiter.Reset("key")

	if !limiter.Allow("key") {
		t.Error("Allow() after Reset() = false, want true")
	}
}

func TestRateLimiterNilIsPermissive(t *testing.T) {
	t.Parallel()

	var limiter *RateLimiter
	if !limiter.Allow("key") {
		t.Error("Allow() on a nil limiter = false, want true")
	}
	limiter.Reset("key") // не должно паниковать
}

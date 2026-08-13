package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	ok, needsRehash, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Error("VerifyPassword() with the correct password = false, want true")
	}
	if needsRehash {
		t.Error("VerifyPassword() needsRehash = true for a hash just created with current parameters")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	ok, _, err := VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if ok {
		t.Error("VerifyPassword() with the wrong password = true, want false")
	}
}

func TestHashPasswordSaltsEachCall(t *testing.T) {
	t.Parallel()

	first, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	second, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if first == second {
		t.Error("two hashes of the same password are identical, want distinct salts")
	}

	// Оба хеша всё равно должны проходить проверку: отличается только соль, а не сам алгоритм.
	for _, hash := range []string{first, second} {
		ok, _, err := VerifyPassword(hash, "same password")
		if err != nil || !ok {
			t.Errorf("VerifyPassword(%q) = %v, %v, want true, nil", hash, ok, err)
		}
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=19456,t=2,p=1$onlysalt",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
	}

	for _, hash := range tests {
		if _, _, err := VerifyPassword(hash, "anything"); err == nil {
			t.Errorf("VerifyPassword(%q) error = nil, want ErrInvalidHash", hash)
		}
	}
}

func TestVerifyDummyAlwaysFails(t *testing.T) {
	t.Parallel()

	// VerifyDummy существует только для того, чтобы вход с неизвестным email
	// тратил столько же процессорного времени, сколько настоящая проверка;
	// функция никогда не должна паниковать и никогда не должна восприниматься
	// вызывающей стороной как успешный вход.
	VerifyDummy("whatever")
	VerifyDummy("")
}

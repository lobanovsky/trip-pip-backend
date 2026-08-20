package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegisterAgencyCreatesUnverifiedUserWithDefaultChannels(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency, user, err := s.RegisterAgency(context.Background(), "Новое агентство", "owner@example.test",
		"hash", "Владелец", []byte("token-hash-1"), time.Hour)
	if err != nil {
		t.Fatalf("RegisterAgency() error = %v", err)
	}

	if user.EmailVerifiedAt != nil {
		t.Errorf("EmailVerifiedAt = %v, want nil", user.EmailVerifiedAt)
	}
	if user.AgencyID != agency.ID {
		t.Errorf("user.AgencyID = %q, want %q", user.AgencyID, agency.ID)
	}

	channels, err := s.ListChannels(context.Background(), agency.ID, false)
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels) != len(DefaultChannels) {
		t.Errorf("len(channels) = %d, want %d (default channels seeded)", len(channels), len(DefaultChannels))
	}
}

func TestVerifyEmailTokenActivatesUserAndCannotBeReused(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	tokenHash := []byte("token-hash-2")
	_, created, err := s.RegisterAgency(context.Background(), "Агентство верификации", "verify@example.test",
		"hash", "Владелец", tokenHash, time.Hour)
	if err != nil {
		t.Fatalf("RegisterAgency() error = %v", err)
	}

	verified, agency, err := s.VerifyEmailToken(context.Background(), tokenHash)
	if err != nil {
		t.Fatalf("VerifyEmailToken() error = %v", err)
	}
	if verified.ID != created.ID {
		t.Errorf("verified.ID = %q, want %q", verified.ID, created.ID)
	}
	if verified.EmailVerifiedAt == nil {
		t.Errorf("EmailVerifiedAt = nil, want set")
	}
	if agency.ID != created.AgencyID {
		t.Errorf("agency.ID = %q, want %q", agency.ID, created.AgencyID)
	}

	if _, _, err := s.VerifyEmailToken(context.Background(), tokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second VerifyEmailToken() error = %v, want ErrNotFound", err)
	}
}

func TestVerifyEmailTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	tokenHash := []byte("token-hash-3")
	if _, _, err := s.RegisterAgency(context.Background(), "Агентство сроков", "expired@example.test",
		"hash", "Владелец", tokenHash, -time.Hour); err != nil {
		t.Fatalf("RegisterAgency() error = %v", err)
	}

	if _, _, err := s.VerifyEmailToken(context.Background(), tokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("VerifyEmailToken() error = %v, want ErrNotFound", err)
	}
}

func TestReissueVerificationTokenInvalidatesOldToken(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	oldToken := []byte("token-hash-4-old")
	if _, _, err := s.RegisterAgency(context.Background(), "Агентство переотправки", "resend@example.test",
		"hash", "Владелец", oldToken, time.Hour); err != nil {
		t.Fatalf("RegisterAgency() error = %v", err)
	}

	newToken := []byte("token-hash-4-new")
	if err := s.ReissueVerificationToken(context.Background(), "resend@example.test", newToken, time.Hour); err != nil {
		t.Fatalf("ReissueVerificationToken() error = %v", err)
	}

	if _, _, err := s.VerifyEmailToken(context.Background(), oldToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("VerifyEmailToken(old) error = %v, want ErrNotFound", err)
	}

	if _, _, err := s.VerifyEmailToken(context.Background(), newToken); err != nil {
		t.Fatalf("VerifyEmailToken(new) error = %v", err)
	}
}

func TestReissueVerificationTokenNotFoundOnceVerified(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	tokenHash := []byte("token-hash-5")
	if _, _, err := s.RegisterAgency(context.Background(), "Агентство подтверждённых", "verified@example.test",
		"hash", "Владелец", tokenHash, time.Hour); err != nil {
		t.Fatalf("RegisterAgency() error = %v", err)
	}
	if _, _, err := s.VerifyEmailToken(context.Background(), tokenHash); err != nil {
		t.Fatalf("VerifyEmailToken() error = %v", err)
	}

	err := s.ReissueVerificationToken(context.Background(), "verified@example.test", []byte("token-hash-5-new"), time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReissueVerificationToken() error = %v, want ErrNotFound (already verified)", err)
	}
}

func TestCreateUserSetsEmailVerifiedAtImmediately(t *testing.T) {
	t.Parallel()

	s := testStore(t)
	agency := createTestAgency(t, s, "Агентство доверенных пользователей")

	user, err := s.CreateUser(context.Background(), agency.ID, "trusted@example.test", "hash", "Коллега")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.EmailVerifiedAt == nil {
		t.Errorf("EmailVerifiedAt = nil, want set (trusted creation path)")
	}
}

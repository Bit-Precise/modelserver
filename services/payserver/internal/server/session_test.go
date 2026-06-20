package server

import (
	"testing"
	"time"
)

func TestSession_Roundtrip(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	s := AdminSession{
		Email:     "ops@example.com",
		Name:      "Ops User",
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second),
	}
	token, err := EncodeSession(s, secret)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeSession(token, secret)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Email != s.Email || got.Name != s.Name {
		t.Errorf("got %+v", got)
	}
	if !got.ExpiresAt.Equal(s.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, s.ExpiresAt)
	}
}

func TestSession_TamperedRejected(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	s := AdminSession{Email: "a@b.c", ExpiresAt: time.Now().Add(time.Hour)}
	token, _ := EncodeSession(s, secret)
	// Flip a byte in the middle
	tampered := token[:len(token)/2] + "X" + token[len(token)/2+1:]
	_, err := DecodeSession(tampered, secret)
	if err == nil {
		t.Error("tampered token decoded without error")
	}
}

func TestSession_WrongSecretRejected(t *testing.T) {
	s := AdminSession{Email: "a@b.c", ExpiresAt: time.Now().Add(time.Hour)}
	token, _ := EncodeSession(s, []byte("secret-A-padded-to-32-byteslong!"))
	_, err := DecodeSession(token, []byte("secret-B-padded-to-32-byteslong!"))
	if err == nil {
		t.Error("decoded with wrong secret")
	}
}

func TestSession_ExpiredRejected(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	s := AdminSession{Email: "a@b.c", ExpiresAt: time.Now().Add(-time.Minute)}
	token, _ := EncodeSession(s, secret)
	_, err := DecodeSession(token, secret)
	if err == nil {
		t.Error("expired token accepted")
	}
}

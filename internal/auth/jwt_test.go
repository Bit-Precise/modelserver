package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWT_GenerateAndValidate(t *testing.T) {
	j := NewJWTManager("test-secret-at-least-32-characters-long", 15*time.Minute, 168*time.Hour)

	access, refresh, err := j.GenerateTokenPair("user-123", "user@example.com", true)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	claims, err := j.ValidateToken(access)
	if err != nil {
		t.Fatalf("validate access failed: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Errorf("UserID = %q, want user-123", claims.UserID)
	}
	if claims.Email != "user@example.com" {
		t.Errorf("Email = %q", claims.Email)
	}
	if claims.TokenType != "access" {
		t.Errorf("TokenType = %q, want access", claims.TokenType)
	}
	if !claims.IsSuperadmin {
		t.Error("expected IsSuperadmin to be true")
	}

	refreshClaims, err := j.ValidateToken(refresh)
	if err != nil {
		t.Fatalf("validate refresh failed: %v", err)
	}
	if refreshClaims.TokenType != "refresh" {
		t.Errorf("TokenType = %q, want refresh", refreshClaims.TokenType)
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	j := NewJWTManager("test-secret-at-least-32-characters-long", -1*time.Second, 168*time.Hour)

	access, _, err := j.GenerateTokenPair("user-123", "user@example.com", false)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	_, err = j.ValidateToken(access)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestJWT_InvalidSecret(t *testing.T) {
	j1 := NewJWTManager("secret-one-that-is-long-enough-32", 15*time.Minute, 168*time.Hour)
	j2 := NewJWTManager("secret-two-that-is-long-enough-32", 15*time.Minute, 168*time.Hour)

	access, _, _ := j1.GenerateTokenPair("user-123", "user@example.com", false)
	_, err := j2.ValidateToken(access)
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestJWT_RejectsNonHS256HMACAlgorithm(t *testing.T) {
	const secret = "test-secret-at-least-32-characters-long"
	j := NewJWTManager(secret, 15*time.Minute, 168*time.Hour)

	claims := Claims{
		UserID:    "user-123",
		Email:     "user@example.com",
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS384, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign HS384 token: %v", err)
	}

	if _, err := j.ValidateToken(token); err == nil {
		t.Fatal("ValidateToken accepted HS384 token; want exact HS256 enforcement")
	}
}

package judgebridge

import (
	"strings"
	"testing"
	"time"
)

func TestSignedClaimsValidation(t *testing.T) {
	now := time.Unix(1800000000, 0)
	secret := strings.Repeat("test", 8)
	claims := Claims{Audience: "cwxu-challenge", CID: 8, StudentName: "测试选手", StudentNumber: "00123", IssuedAt: now.Unix(), ExpiresAt: now.Unix() + 60}
	token, err := Sign(claims, secret)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Verify(token, secret, claims.Audience, now); err != nil || got.StudentNumber != "00123" {
		t.Fatalf("valid token: %v", err)
	}
	for name, check := range map[string]func() error{
		"wrong secret":   func() error { _, e := Verify(token, strings.Repeat("x", 32), claims.Audience, now); return e },
		"wrong audience": func() error { _, e := Verify(token, secret, "cwxu-proof", now); return e },
		"expired":        func() error { _, e := Verify(token, secret, claims.Audience, now.Add(61*time.Second)); return e },
		"future":         func() error { _, e := Verify(token, secret, claims.Audience, now.Add(-6*time.Second)); return e },
		"tampered":       func() error { _, e := Verify(token+"x", secret, claims.Audience, now); return e },
	} {
		if check() == nil {
			t.Error(name + " accepted")
		}
	}
	claims.ExpiresAt++
	long, _ := Sign(claims, secret)
	if _, err := Verify(long, secret, claims.Audience, now); err == nil {
		t.Fatal("excessive lifetime accepted")
	}
}

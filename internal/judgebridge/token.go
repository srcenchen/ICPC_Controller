// Package judgebridge defines the signed OJ/client-presence protocol.
package judgebridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Claims struct {
	Audience      string `json:"aud"`
	Nonce         string `json:"nonce,omitempty"`
	SessionHash   string `json:"session_hash,omitempty"`
	UID           int    `json:"uid,omitempty"`
	CID           int    `json:"cid"`
	StudentNumber string `json:"student_number"`
	StudentName   string `json:"student_name"`
	Origin        string `json:"origin,omitempty"`
	Purpose       string `json:"purpose,omitempty"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
	ChallengeHash string `json:"challenge_hash,omitempty"`
	DeviceID      int    `json:"device_id,omitempty"`
	NodeID        string `json:"node_id,omitempty"`
	EventID       string `json:"event_id,omitempty"`
	AccessKind    string `json:"access_kind,omitempty"`
	LoggedInAt    int64  `json:"logged_in_at,omitempty"`
}

func Sign(claims Claims, secret string) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("OJ 共享密钥未配置")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	message := "v1." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return message + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Unverified only extracts routing information at the local bridge. Its result
// must never authorize a login or submission without Verify at a trusted server.
func Unverified(token string) (Claims, error) {
	var claims Claims
	if len(token) > 8192 {
		return claims, errors.New("验证请求过大")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return claims, errors.New("验证请求格式无效")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, errors.New("验证请求编码无效")
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errors.New("验证请求内容无效")
	}
	return claims, nil
}

func Verify(token, secret, audience string, now time.Time) (Claims, error) {
	var empty Claims
	if len(secret) < 32 {
		return empty, errors.New("OJ 共享密钥未配置")
	}
	claims, err := Unverified(token)
	if err != nil {
		return empty, err
	}
	parts := strings.Split(token, ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return empty, errors.New("验证请求签名无效")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return empty, errors.New("验证请求签名无效")
	}
	if claims.Audience != audience || claims.IssuedAt > now.Unix()+5 || claims.ExpiresAt < now.Unix() ||
		claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > 60 {
		return empty, errors.New("验证请求已过期或用途不符")
	}
	return claims, nil
}

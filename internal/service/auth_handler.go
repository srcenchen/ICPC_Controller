package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RateLimitEntry holds attempts and block timestamps.
type RateLimitEntry struct {
	Attempts     int
	BlockedUntil time.Time
}

// LoginRateLimiter enforces brute-force protection.
type LoginRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*RateLimitEntry
}

// NewLoginRateLimiter initializes a rate limiter.
func NewLoginRateLimiter() *LoginRateLimiter {
	return &LoginRateLimiter{
		entries: make(map[string]*RateLimitEntry),
	}
}

// Allow checks if the IP is allowed to attempt login.
func (l *LoginRateLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	entry, exists := l.entries[ip]
	if !exists {
		return true, 0
	}

	if entry.BlockedUntil.After(now) {
		return false, entry.BlockedUntil.Sub(now)
	}

	// Reset attempts if the block duration has expired
	if entry.BlockedUntil.Before(now) && entry.Attempts >= 5 {
		entry.Attempts = 0
	}

	return true, 0
}

// RecordFailure increments attempts and blocks if threshold is reached.
func (l *LoginRateLimiter) RecordFailure(ip string) (int, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	entry, exists := l.entries[ip]
	if !exists {
		entry = &RateLimitEntry{}
		l.entries[ip] = entry
	}

	entry.Attempts++
	if entry.Attempts >= 5 {
		blockDuration := 5 * time.Minute
		entry.BlockedUntil = now.Add(blockDuration)
		return entry.Attempts, blockDuration
	}

	return entry.Attempts, 0
}

// RecordSuccess clears attempts upon successful login.
func (l *LoginRateLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.entries, ip)
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return rip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// LoginRequest holds credentials for login.
type LoginRequest struct {
	Password string `json:"password"`
}

// ChangePasswordRequest holds payload for password updates.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// AuthHandler manages admin authentication and session tokens.
type AuthHandler struct {
	settings *ServerSettings
	jwtKey   []byte
	limiter  *LoginRateLimiter
}

// NewAuthHandler creates a new AuthHandler with a static signing key.
func NewAuthHandler(settings *ServerSettings) *AuthHandler {
	return &AuthHandler{
		settings: settings,
		jwtKey:   []byte("icpc-remote-control-jwt-secret-key-signature"),
		limiter:  NewLoginRateLimiter(),
	}
}

// Login handles admin authentication (POST /api/auth/login).
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	ip := getClientIP(r)

	// Check if IP is blocked
	if allowed, blockDuration := h.limiter.Allow(ip); !allowed {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("登录失败次数过多，请在 %d 秒后再试", int(blockDuration.Seconds())),
		})
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if !h.settings.VerifyPassword(req.Password) {
		attempts, blockDuration := h.limiter.RecordFailure(ip)
		if blockDuration > 0 {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "密码错误。尝试失败次数过多，该 IP 已被锁定 5 分钟",
			})
		} else {
			remaining := 5 - attempts
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": fmt.Sprintf("密码不正确，剩余尝试次数：%d", remaining),
			})
		}
		return
	}

	// Reset rate limits on success
	h.limiter.RecordSuccess(ip)

	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(expirationTime),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		Subject:   "admin",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(h.jwtKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	// Set JWT as a HTTP cookie for automatic page/websocket handshakes authentication
	http.SetCookie(w, &http.Cookie{
		Name:     "jwt_token",
		Value:    tokenString,
		Expires:  expirationTime,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{"token": tokenString})
}

// Logout invalidates the user cookie session (POST /api/auth/logout).
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "jwt_token",
		Value:    "",
		Expires:  time.Unix(0, 0),
		Path:     "/",
		HttpOnly: true,
	})
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// ChangePassword updates the admin password (POST /api/auth/password).
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "新密码不能为空"})
		return
	}

	if !h.settings.VerifyPassword(req.OldPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "旧密码不正确"})
		return
	}

	if err := h.settings.SetAdminPassword(req.NewPassword); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "密码修改成功"})
}

// AuthMiddleware wraps a handler enforcing JWT verification.
func (h *AuthHandler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Exclude public paths
		if path == "/login.html" ||
			path == "/api/auth/login" ||
			strings.HasPrefix(path, "/assets/") ||
			strings.HasPrefix(path, "/broadcast/") ||
			path == "/ws/broadcast" {
			next.ServeHTTP(w, r)
			return
		}

		// Check Cookie first (for page visits / websockets)
		var tokenStr string
		cookie, err := r.Cookie("jwt_token")
		if err == nil {
			tokenStr = cookie.Value
		} else {
			// Check Authorization Header (for AJAX requests)
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if tokenStr == "" {
			h.unauthorized(w, r)
			return
		}

		claims := &jwt.RegisteredClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return h.jwtKey, nil
		})

		if err != nil || !token.Valid {
			h.unauthorized(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *AuthHandler) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
	} else {
		http.Redirect(w, r, "/login.html", http.StatusFound)
	}
}

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	sessionCookieName = "webchecker_session"
	envSubject        = "env"
)

type sessionClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func issueToken(subject, username, secret string, ttl time.Duration) (string, time.Time, error) {
	if secret == "" {
		return "", time.Time{}, errors.New("empty jwt secret")
	}
	exp := time.Now().UTC().Add(ttl)
	claims := sessionClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	raw, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", time.Time{}, err
	}
	return raw, exp, nil
}

func parseToken(raw, secret string) (sessionClaims, error) {
	var claims sessionClaims
	token, err := jwt.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return sessionClaims{}, errors.New("invalid token")
	}
	if claims.Subject == "" || claims.Username == "" {
		return sessionClaims{}, errors.New("invalid claims")
	}
	return claims, nil
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		return strings.TrimSpace(c.Value)
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, token string, exp time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func subjectUserID(sub string) (int64, bool) {
	if sub == "" || sub == envSubject {
		return 0, false
	}
	id, err := strconv.ParseInt(sub, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

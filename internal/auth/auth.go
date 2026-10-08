// Package auth protects the MCP endpoint with pluggable authentication
// strategies. Version 1 ships static API keys; an OAuth token validator can be
// added later as another Authenticator.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

var (
	// ErrNoCredentials means the request carried no credentials at all.
	ErrNoCredentials = errors.New("no credentials")
	// ErrInvalidCredentials means the credentials were present but wrong.
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Identity describes an authenticated caller. ID is safe to log.
type Identity struct {
	ID string
}

// Authenticator validates the credentials of a request.
type Authenticator interface {
	Authenticate(r *http.Request) (Identity, error)
}

// Credential extracts the presented credential from "Authorization: Bearer"
// or "X-API-Key". The bearer token takes precedence.
func Credential(h http.Header) string {
	if v := h.Get("Authorization"); v != "" {
		scheme, token, ok := strings.Cut(v, " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(token)
		}
	}
	return strings.TrimSpace(h.Get("X-API-Key"))
}

// KeyID returns a short, non-reversible identifier of a key for logging:
// the first 8 hex characters of its SHA-256 hash.
func KeyID(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// HeaderKeyID returns KeyID of the credential presented in h.
func HeaderKeyID(h http.Header) string { return KeyID(Credential(h)) }

// StaticKeys authenticates requests against a fixed set of API keys.
type StaticKeys struct {
	hashes [][sha256.Size]byte
}

// NewStaticKeys returns an Authenticator for the given keys.
func NewStaticKeys(keys []string) *StaticKeys {
	s := &StaticKeys{}
	for _, k := range keys {
		s.hashes = append(s.hashes, sha256.Sum256([]byte(k)))
	}
	return s
}

// Authenticate compares the presented key in constant time against every
// configured key. Hashing first makes the comparison independent of the key
// length.
func (s *StaticKeys) Authenticate(r *http.Request) (Identity, error) {
	cred := Credential(r.Header)
	if cred == "" {
		return Identity{}, ErrNoCredentials
	}
	sum := sha256.Sum256([]byte(cred))
	match := 0
	for i := range s.hashes {
		match |= subtle.ConstantTimeCompare(sum[:], s.hashes[i][:])
	}
	if match != 1 {
		return Identity{}, ErrInvalidCredentials
	}
	return Identity{ID: KeyID(cred)}, nil
}

type ctxKey struct{}

// IdentityFrom returns the identity stored by Middleware, if any.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// Middleware rejects unauthenticated requests with 401. A nil Authenticator
// lets every request through (open mode).
func Middleware(a Authenticator, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if a == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := a.Authenticate(r)
			if err != nil {
				attrs := []any{
					slog.String("reason", err.Error()),
					slog.String("remote_addr", r.RemoteAddr),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				}
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					attrs = append(attrs, slog.String("forwarded_for", xff))
				}
				if errors.Is(err, ErrInvalidCredentials) {
					attrs = append(attrs, slog.String("key_id", HeaderKeyID(r.Header)))
				}
				logger.WarnContext(r.Context(), "authentication failed", attrs...)
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="lantern"`)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","message":"missing or invalid API key"}` + "\n"))
}

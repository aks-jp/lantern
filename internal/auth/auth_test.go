package auth

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCredential(t *testing.T) {
	tests := []struct {
		name   string
		header map[string]string
		want   string
	}{
		{"bearer", map[string]string{"Authorization": "Bearer abc"}, "abc"},
		{"bearer lowercase", map[string]string{"Authorization": "bearer abc "}, "abc"},
		{"x-api-key", map[string]string{"X-API-Key": "def"}, "def"},
		{"bearer wins", map[string]string{"Authorization": "Bearer abc", "X-API-Key": "def"}, "abc"},
		{"basic ignored", map[string]string{"Authorization": "Basic Zm9vOmJhcg==", "X-API-Key": "def"}, "def"},
		{"none", map[string]string{}, ""},
	}
	for _, tt := range tests {
		h := http.Header{}
		for k, v := range tt.header {
			h.Set(k, v)
		}
		if got := Credential(h); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestKeyID(t *testing.T) {
	if KeyID("") != "" {
		t.Error("empty key must have empty id")
	}
	id := KeyID("secret")
	if len(id) != 8 || strings.Contains(id, "secret") || id != KeyID("secret") || id == KeyID("secret2") {
		t.Errorf("unexpected id %q", id)
	}
}

func TestStaticKeys(t *testing.T) {
	a := NewStaticKeys([]string{"key-one", "key-two-is-longer"})
	tests := []struct {
		name    string
		header  string
		value   string
		wantErr error
	}{
		{"no key", "", "", ErrNoCredentials},
		{"wrong key", "Authorization", "Bearer nope", ErrInvalidCredentials},
		{"prefix of key", "X-API-Key", "key-", ErrInvalidCredentials},
		{"bearer", "Authorization", "Bearer key-one", nil},
		{"x-api-key", "X-API-Key", "key-two-is-longer", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tt.header != "" {
				r.Header.Set(tt.header, tt.value)
			}
			id, err := a.Authenticate(r)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && id.ID != KeyID(Credential(r.Header)) {
				t.Errorf("id = %q", id.ID)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	var gotID Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, _ = IdentityFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := Middleware(NewStaticKeys([]string{"valid-key"}), logger)(next)

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-API-Key", "wrong-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != `Bearer realm="lantern"` ||
		!strings.Contains(w.Body.String(), `"unauthorized"`) {
		t.Errorf("rejection = %d %v %s", w.Code, w.Header(), w.Body)
	}
	if strings.Contains(logs.String(), "wrong-secret") || !strings.Contains(logs.String(), "remote_addr") {
		t.Errorf("log must contain the client address but never the key: %s", logs.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer valid-key")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || gotID.ID != KeyID("valid-key") {
		t.Errorf("accepted request = %d, id %q", w.Code, gotID.ID)
	}
}

func TestMiddlewareOpenMode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	w := httptest.NewRecorder()
	Middleware(nil, slog.New(slog.DiscardHandler))(next).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("open mode must pass requests through, got %d", w.Code)
	}
}

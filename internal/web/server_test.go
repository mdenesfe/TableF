package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeService struct{ ctx context.Context }

func (*fakeService) Add(a, b int) (int, error) { return a + b, nil }
func (*fakeService) Fail() error               { return errors.New("boom") }
func (*fakeService) Shutdown()                 {}

// Ctx reports whether the call got the request's context.
func (s *fakeService) Ctx() (bool, error) { return s.ctx != nil && s.ctx.Value(ctxKey{}) == "req", nil }

type ctxKey struct{}

var assets = fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}

func call(t *testing.T, h http.Handler, host, method, path, body string, header map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestRPC(t *testing.T) {
	h := NewHandler(&fakeService{}, assets, "")
	rec, out := call(t, h, "127.0.0.1:7070", http.MethodPost, "/api/rpc/Add", "[2, 3]", nil)
	if rec.Code != http.StatusOK || out["result"] != float64(5) {
		t.Fatalf("Add: %d %v", rec.Code, out)
	}
	if _, out := call(t, h, "localhost", http.MethodPost, "/api/rpc/Fail", "[]", nil); out["error"] != "boom" {
		t.Fatalf("Fail: %v", out)
	}
}

func TestRPCRejects(t *testing.T) {
	h := NewHandler(&fakeService{}, assets, "")
	cases := []struct {
		name, method, path, body, ctype string
		want                            int
	}{
		{"GET", http.MethodGet, "/api/rpc/Add", "", "application/json", http.StatusMethodNotAllowed},
		{"form post", http.MethodPost, "/api/rpc/Add", "a=1", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"unknown", http.MethodPost, "/api/rpc/Nope", "[]", "application/json", http.StatusNotFound},
		{"hidden", http.MethodPost, "/api/rpc/Shutdown", "[]", "application/json", http.StatusNotFound},
		{"too few args", http.MethodPost, "/api/rpc/Add", "[1]", "application/json", http.StatusBadRequest},
		{"too many args", http.MethodPost, "/api/rpc/Add", "[1, 2, 3]", "application/json", http.StatusBadRequest},
		{"bad arg type", http.MethodPost, "/api/rpc/Add", `["a", 2]`, "application/json", http.StatusBadRequest},
		{"not json", http.MethodPost, "/api/rpc/Add", "{", "application/json", http.StatusBadRequest},
		{"too large", http.MethodPost, "/api/rpc/Add", "[" + strings.Repeat(" ", MaxBodyBytes) + "1, 2]", "application/json", http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		rec, _ := call(t, h, "localhost:7070", c.method, c.path, c.body, map[string]string{"Content-Type": c.ctype})
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestHostCheckWithoutToken(t *testing.T) {
	h := NewHandler(&fakeService{}, assets, "")
	for _, host := range []string{"localhost", "localhost:7070", "LOCALHOST:1", "127.0.0.1:7070", "[::1]:7070", "[::1]"} {
		if rec, _ := call(t, h, host, http.MethodPost, "/api/rpc/Add", "[1, 1]", nil); rec.Code != http.StatusOK {
			t.Errorf("host %q: status %d", host, rec.Code)
		}
	}
	// DNS rebinding: an attacker's name resolving to 127.0.0.1.
	for _, host := range []string{"evil.example:7070", "localhost.evil.example", "127.0.0.2:7070", "192.168.1.5:7070"} {
		for _, p := range []string{"/api/rpc/Add", "/api/info", "/"} {
			if rec, _ := call(t, h, host, http.MethodPost, p, "[1, 1]", nil); rec.Code != http.StatusForbidden {
				t.Errorf("host %q %s: status %d, want 403", host, p, rec.Code)
			}
		}
	}
}

func TestToken(t *testing.T) {
	h := NewHandler(&fakeService{}, assets, "s3cret")
	// With a token any host is fine, but the token is required.
	if rec, _ := call(t, h, "db.example:7070", http.MethodPost, "/api/rpc/Add", "[1, 1]", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	for _, auth := range []string{"Bearer wrong", "s3cret", "Basic s3cret"} {
		if rec, _ := call(t, h, "db.example", http.MethodPost, "/api/rpc/Add", "[1, 1]", map[string]string{"Authorization": auth}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%q: %d", auth, rec.Code)
		}
	}
	rec, out := call(t, h, "db.example", http.MethodPost, "/api/rpc/Add", "[1, 1]", map[string]string{"Authorization": "Bearer s3cret"})
	if rec.Code != http.StatusOK || out["result"] != float64(2) {
		t.Fatalf("with token: %d %v", rec.Code, out)
	}
	if _, out := call(t, h, "db.example", http.MethodGet, "/api/info", "", nil); out["auth"] != true {
		t.Fatalf("info: %v", out)
	}
}

func TestBindContext(t *testing.T) {
	h := NewHandler(&fakeService{}, assets, "").BindContext(func(ctx context.Context) any {
		return &fakeService{ctx: ctx}
	})
	req := httptest.NewRequest(http.MethodPost, "/api/rpc/Ctx", strings.NewReader("[]"))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, "req"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"result":true`) {
		t.Fatalf("method did not get the request context: %s", rec.Body)
	}
}

func TestStatic(t *testing.T) {
	h := NewHandler(&fakeService{}, assets, "")
	for _, p := range []string{"/", "/some/route"} {
		rec, _ := call(t, h, "localhost", http.MethodGet, p, "", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body)
		}
	}
}

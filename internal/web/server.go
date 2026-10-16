// Package web exposes core.Service over HTTP for the browser build.
//
// Every exported Service method is available as POST /api/rpc/<Method> with a
// JSON array of arguments as the body. This mirrors the Wails bindings, so the
// frontend calls the same method names on desktop and web.
package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"reflect"
	"strings"
)

// MaxBodyBytes caps an RPC request body.
const MaxBodyBytes = 32 << 20

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// hidden methods are lifecycle hooks, not API.
var hidden = map[string]bool{"Shutdown": true}

// Handler serves the RPC API and the static frontend.
type Handler struct {
	target  reflect.Value
	methods map[string]reflect.Method
	assets  fs.FS
	token   string
	// bind returns the receiver for one request; by default target itself.
	bind func(ctx context.Context) reflect.Value
}

// NewHandler builds a handler for service. If token is non-empty every API
// request must send "Authorization: Bearer <token>". Without a token only
// requests addressed to localhost are served, which defeats DNS rebinding
// (a malicious site resolving its own name to 127.0.0.1).
func NewHandler(service any, assets fs.FS, token string) *Handler {
	v := reflect.ValueOf(service)
	t := v.Type()
	methods := map[string]reflect.Method{}
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if !hidden[m.Name] {
			methods[m.Name] = m
		}
	}
	return &Handler{target: v, methods: methods, assets: assets, token: token}
}

// BindContext makes each RPC call run on bind(r.Context()) instead of the
// service passed to NewHandler, so work stops when the client goes away.
// bind must return a value of the same type as that service.
func (h *Handler) BindContext(bind func(ctx context.Context) any) *Handler {
	h.bind = func(ctx context.Context) reflect.Value { return reflect.ValueOf(bind(ctx)) }
	return h
}

// ServeHTTP routes /api/info, /api/rpc/<Method> and the static frontend.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.token == "" && !isLocalHost(r.Host) {
		writeError(w, http.StatusForbidden, errors.New("forbidden host; start tablef-web with -token to serve other hosts"))
		return
	}
	switch {
	case r.URL.Path == "/api/info":
		writeJSON(w, http.StatusOK, map[string]any{"auth": h.token != ""})
	case strings.HasPrefix(r.URL.Path, "/api/rpc/"):
		h.rpc(w, r)
	default:
		h.static(w, r)
	}
}

// isLocalHost reports whether a Host header names this machine's loopback.
func isLocalHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func (h *Handler) authorized(r *http.Request) bool {
	if h.token == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

func (h *Handler) rpc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, errors.New("POST required"))
		return
	}
	// Reject cross-site form posts; the frontend always sends JSON.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, errors.New("application/json required"))
		return
	}
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/rpc/")
	m, ok := h.methods[name]
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("unknown method "+name))
		return
	}

	var raw []json.RawMessage
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("request body too large"))
		return
	}
	if err == nil && len(body) > 0 {
		err = json.Unmarshal(body, &raw)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	nIn := m.Type.NumIn() - 1 // minus receiver
	if len(raw) != nIn {
		writeError(w, http.StatusBadRequest, errors.New("wrong number of arguments"))
		return
	}
	recv := h.target
	if h.bind != nil {
		recv = h.bind(r.Context())
	}
	args := make([]reflect.Value, 0, nIn+1)
	args = append(args, recv)
	for i := 0; i < nIn; i++ {
		p := reflect.New(m.Type.In(i + 1))
		if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		args = append(args, p.Elem())
	}

	out := m.Func.Call(args)
	var result any
	for _, v := range out {
		if v.Type().Implements(errorType) {
			if !v.IsNil() {
				writeError(w, http.StatusOK, v.Interface().(error))
				return
			}
			continue
		}
		result = v.Interface()
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := h.assets.Open(p); err == nil {
		st, _ := f.Stat()
		f.Close()
		if st != nil && !st.IsDir() {
			if strings.HasPrefix(p, "_app/immutable/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, h.assets, p)
			return
		}
	}
	// SPA fallback
	http.ServeFileFS(w, r, h.assets, "index.html")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.4", "0.1.3", true},
		{"v0.1.10", "0.1.9", true},
		{"0.2", "0.1.9", true},
		{"0.1.3", "0.1.3", false},
		{"0.1.2", "0.1.3", false},
		{"1.0.0", "1.0.0-beta", true},
		{"1.0.0-beta", "1.0.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestDownloadChecksDigest(t *testing.T) {
	body := strings.Repeat("TableF", 100_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	sum := sha256.Sum256([]byte(body))
	dir := t.TempDir()

	var last int64
	path, err := Download(context.Background(), Asset{Name: "u.zip", URL: srv.URL, Size: int64(len(body)), Digest: "sha256:" + hex.EncodeToString(sum[:])}, dir,
		func(done, _ int64) { last = done })
	if err != nil || last != int64(len(body)) {
		t.Fatalf("download: %v (progress %d)", err, last)
	}
	if b, _ := os.ReadFile(path); string(b) != body {
		t.Fatal("content differs")
	}

	_, err = Download(context.Background(), Asset{Name: "bad.zip", URL: srv.URL, Digest: "sha256:" + strings.Repeat("0", 64)}, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a wrong digest must be refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.zip")); !os.IsNotExist(err) {
		t.Fatal("a refused download must not be kept")
	}
	if _, err := Download(context.Background(), Asset{Name: "x.zip", URL: srv.URL}, dir, nil); err == nil {
		t.Fatal("a release without a digest must be refused")
	}
}

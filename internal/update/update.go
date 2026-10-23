// Package update keeps the desktop app current: it asks GitHub for the latest
// release, downloads this platform's build, checks it (SHA-256 from GitHub,
// and on macOS the Developer ID signature) and swaps it in.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases are published.
const Repo = "mdenesfe/TableF"

// TeamID is the Apple Developer team the macOS app is signed by; a download
// signed by anyone else is refused.
const TeamID = "8F6VQL85M6"

// Asset is one downloadable file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
	// Digest is GitHub's checksum, e.g. "sha256:ab12…".
	Digest string `json:"digest"`
}

// Release is a published version.
type Release struct {
	Version     string `json:"version"` // without the leading v
	Name        string `json:"name"`
	Notes       string `json:"notes"` // Markdown
	URL         string `json:"url"`   // the release page
	PublishedAt string `json:"publishedAt"`
	// Asset is this platform's build; nil when the release has none.
	Asset *Asset `json:"asset"`
}

// assetName is the release file for this platform's in-place update.
func assetName() string {
	switch runtime.GOOS {
	case "darwin":
		return "TableF-macos-universal.zip"
	case "windows":
		return "TableF-windows-amd64.zip"
	}
	return ""
}

var client = &http.Client{Timeout: 30 * time.Second}

// Latest asks GitHub for the newest published (non-draft, non-prerelease) release.
func Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "TableF")
	res, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("checking for updates: GitHub answered %s", res.Status)
	}
	var gh struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&gh); err != nil {
		return Release{}, err
	}
	r := Release{
		Version:     strings.TrimPrefix(gh.TagName, "v"),
		Name:        gh.Name,
		Notes:       gh.Body,
		URL:         gh.HTMLURL,
		PublishedAt: gh.PublishedAt,
	}
	for _, a := range gh.Assets {
		if a.Name == assetName() {
			r.Asset = &Asset{Name: a.Name, URL: a.URL, Size: a.Size, Digest: a.Digest}
		}
	}
	return r, nil
}

// Newer reports whether version a is newer than b ("0.1.10" > "0.1.9").
// Missing parts count as 0; anything after a "-" (a pre-release) sorts
// before the plain version.
func Newer(a, b string) bool {
	pa, pb := parse(a), parse(b)
	for i := range 3 {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	return pa.pre == "" && pb.pre != "" || pa.pre != "" && pb.pre != "" && pa.pre > pb.pre
}

type version struct {
	n   [3]int
	pre string
}

func parse(v string) version {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out version
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, out.pre = v[:i], v[i+1:]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out.n[i], _ = strconv.Atoi(p)
	}
	return out
}

// Download fetches an asset into dir and checks it against GitHub's digest.
// progress (optional) gets the bytes done and the total as it goes.
func Download(ctx context.Context, a Asset, dir string, progress func(done, total int64)) (string, error) {
	want, ok := strings.CutPrefix(a.Digest, "sha256:")
	if !ok || len(want) != 64 {
		return "", errors.New("the release has no SHA-256 digest to check the download against")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "TableF")
	res, err := (&http.Client{}).Do(req) // no overall timeout: large file
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading the update: %s", res.Status)
	}
	path := filepath.Join(dir, a.Name)
	f, err := os.OpenFile(path+".part", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	total := a.Size
	if total <= 0 {
		total = res.ContentLength
	}
	var done int64
	buf := make([]byte, 256<<10)
	var last time.Time
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil && time.Since(last) > 150*time.Millisecond {
				progress(done, total)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if progress != nil {
		progress(done, total)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(path + ".part")
		return "", errors.New("the downloaded update doesn't match its published checksum; it was not installed")
	}
	if err := os.Rename(path+".part", path); err != nil {
		return "", err
	}
	return path, nil
}

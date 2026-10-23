package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveRelease downloads the latest macOS release from GitHub and runs the
// checks Install does before swapping it in (digest, signature by our team,
// notarization). Opt in: TABLEF_LIVE_UPDATE_TEST=1 go test ./internal/update
func TestLiveRelease(t *testing.T) {
	if os.Getenv("TABLEF_LIVE_UPDATE_TEST") == "" {
		t.Skip("set TABLEF_LIVE_UPDATE_TEST=1 to download the latest release")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := Latest(ctx)
	if err != nil || r.Asset == nil {
		t.Fatalf("latest: %+v %v", r, err)
	}
	t.Logf("latest %s, %s (%d bytes, %s)", r.Version, r.Asset.Name, r.Asset.Size, r.Asset.Digest)
	dir := t.TempDir()
	path, err := Download(ctx, *r.Asset, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(dir, "stage")
	if out, err := exec.Command("ditto", "-x", "-k", path, stage).CombinedOutput(); err != nil {
		t.Fatalf("unpack: %v %s", err, out)
	}
	app := filepath.Join(stage, "TableF.app")
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", "-R="+requirement, app).CombinedOutput(); err != nil {
		t.Fatalf("signature: %s", out)
	}
	if out, err := exec.Command("spctl", "--assess", "--type", "execute", app).CombinedOutput(); err != nil {
		t.Fatalf("gatekeeper: %s", out)
	}
	// Install over a stand-in for the running app, as InstallUpdate does.
	installed := filepath.Join(dir, "Applications", "TableF.app")
	if err := os.MkdirAll(filepath.Join(installed, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(installed, "Contents", "old-version"), nil, 0o644)
	got, err := installInto(ctx, path, installed)
	if err != nil || got != installed {
		t.Fatalf("install: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(installed, "Contents", "old-version")); !os.IsNotExist(err) {
		t.Fatal("the old bundle should be gone")
	}
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", "-R="+requirement, installed).CombinedOutput(); err != nil {
		t.Fatalf("installed app signature: %s", out)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "Applications", ".tablef-update-*")); len(left) != 0 || exists(installed+".old") {
		t.Fatalf("leftovers: %v", left)
	}

	// A different team must be refused by the same check.
	other := `anchor apple generic and certificate leaf[subject.OU] = "ZZZZZZZZZZ"`
	if err := exec.Command("codesign", "--verify", "-R="+other, app).Run(); err == nil {
		t.Fatal("the requirement must reject another team")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

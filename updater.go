package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mdenesfe/TableF/internal/update"
)

// Updater checks GitHub for a newer TableF, downloads it and installs it.
// The UI drives it: check on start and every few hours, download in the
// background, then install and restart when the user says so.
type Updater struct {
	ctx context.Context

	mu         sync.Mutex
	release    *update.Release // the newer release found by the last check
	downloaded string          // its verified download, once fetched
	busy       bool            // a download is running
}

// UpdateCheck is the result of CheckUpdate.
type UpdateCheck struct {
	Available bool            `json:"available"`
	Release   *update.Release `json:"release"`
	// CanInstall: this build can update itself in place (macOS, Windows).
	CanInstall bool `json:"canInstall"`
	// Downloaded: the update is fetched and ready to install.
	Downloaded bool `json:"downloaded"`
}

func (u *Updater) startup(ctx context.Context) {
	u.ctx = ctx
	update.Cleanup()
}

func canInstall() bool { return goruntime.GOOS == "darwin" || goruntime.GOOS == "windows" }

// CheckUpdate asks GitHub whether a release newer than current exists.
func (u *Updater) CheckUpdate(current string) (UpdateCheck, error) {
	r, err := update.Latest(u.ctx)
	if err != nil {
		return UpdateCheck{}, err
	}
	out := UpdateCheck{CanInstall: canInstall() && r.Asset != nil}
	if !update.Newer(r.Version, current) {
		return out, nil
	}
	out.Available, out.Release = true, &r
	u.mu.Lock()
	if u.release == nil || u.release.Version != r.Version {
		u.release, u.downloaded = &r, ""
	}
	out.Downloaded = u.downloaded != ""
	u.mu.Unlock()
	return out, nil
}

// DownloadUpdate fetches the release found by CheckUpdate into the user's
// cache folder and checks its SHA-256. Progress goes out as "update:progress"
// events ({done, total} in bytes).
func (u *Updater) DownloadUpdate() error {
	u.mu.Lock()
	r := u.release
	if r == nil || r.Asset == nil {
		u.mu.Unlock()
		return errors.New("no update to download; check for updates first")
	}
	if u.downloaded != "" {
		u.mu.Unlock()
		return nil
	}
	if u.busy {
		u.mu.Unlock()
		return errors.New("the update is already downloading")
	}
	u.busy = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()

	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(cache, "TableF", "updates", r.Version)
	path, err := update.Download(u.ctx, *r.Asset, dir, func(done, total int64) {
		runtime.EventsEmit(u.ctx, "update:progress", map[string]int64{"done": done, "total": total})
	})
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.release == r {
		u.downloaded = path
	}
	u.mu.Unlock()
	return nil
}

// InstallUpdate swaps in the downloaded release and restarts TableF.
func (u *Updater) InstallUpdate() error {
	u.mu.Lock()
	path := u.downloaded
	u.mu.Unlock()
	if path == "" {
		return errors.New("the update isn't downloaded yet")
	}
	target, err := update.Install(u.ctx, path)
	if err != nil {
		return err
	}
	if err := update.Relaunch(target); err != nil {
		return err
	}
	runtime.Quit(u.ctx)
	return nil
}

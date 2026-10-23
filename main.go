// Command TableF is the desktop (macOS / Windows / Linux) build, powered by Wails.
package main

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mdenesfe/TableF/internal/core"
	_ "github.com/mdenesfe/TableF/internal/engines"
	"github.com/mdenesfe/TableF/internal/ui"
)

// Desktop holds features that only exist in the native app, like file dialogs.
type Desktop struct {
	ctx context.Context
}

// PickSQLiteFile opens a native file picker and returns the chosen path.
func (d *Desktop) PickSQLiteFile() (string, error) {
	return runtime.OpenFileDialog(d.ctx, runtime.OpenDialogOptions{
		Title: "Open SQLite database",
		Filters: []runtime.FileFilter{
			{DisplayName: "SQLite (*.db;*.sqlite;*.sqlite3)", Pattern: "*.db;*.sqlite;*.sqlite3;*.db3"},
			{DisplayName: "All files", Pattern: "*"},
		},
	})
}

// PickKeyFile opens a native file picker for an SSH private key.
func (d *Desktop) PickKeyFile() (string, error) {
	home, _ := os.UserHomeDir()
	return runtime.OpenFileDialog(d.ctx, runtime.OpenDialogOptions{
		Title:                "Choose SSH private key",
		DefaultDirectory:     filepath.Join(home, ".ssh"),
		ShowHiddenFiles:      true,
		CanCreateDirectories: false,
	})
}

// SaveTextFile asks where to save and writes content there. It reports
// false when the user cancels.
func (d *Desktop) SaveTextFile(name, content string) (bool, error) {
	return d.writeFile(name, []byte(content))
}

// SaveBinaryFile is SaveTextFile for binary content (e.g. .xlsx), passed as base64.
func (d *Desktop) SaveBinaryFile(name, b64 string) (bool, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return false, err
	}
	return d.writeFile(name, data)
}

// writeFile asks where to save name and writes data there, readable only by
// the user since exports often contain database contents.
func (d *Desktop) writeFile(name string, data []byte) (bool, error) {
	path, err := runtime.SaveFileDialog(d.ctx, runtime.SaveDialogOptions{
		Title:                "Save",
		DefaultFilename:      name,
		CanCreateDirectories: true,
	})
	if err != nil || path == "" {
		return false, err
	}
	return true, os.WriteFile(path, data, 0o600)
}

// appMenu is the macOS menu bar. It keeps the standard app and Edit menus
// (copy/paste need them) but leaves out Window, whose ⌘W would close the
// whole window instead of reaching the app, where ⌘W closes a tab.
func appMenu() *menu.Menu {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	m := menu.NewMenu()
	m.Append(menu.AppMenu())
	m.Append(menu.EditMenu())
	return m
}

func main() {
	storePath, err := core.DefaultStorePath()
	if err != nil {
		log.Fatal(err)
	}
	svc := core.NewService(core.NewStore(storePath))
	desktop := &Desktop{}
	updater := &Updater{}

	err = wails.Run(&options.App{
		Title:     "TableF",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 560,
		// The brand color: the window opens in it, matching the start-up animation.
		BackgroundColour: &options.RGBA{R: 10, G: 61, B: 98, A: 1},
		AssetServer:      &assetserver.Options{Assets: ui.Assets()},
		OnStartup: func(ctx context.Context) {
			desktop.ctx = ctx
			updater.startup(ctx)
			manageWindowChrome(ctx)
		},
		OnShutdown: func(context.Context) { svc.Shutdown() },
		Bind:       []any{svc, desktop, updater},
		Menu:       appMenu(),
		// Off macOS the header draws its own window controls (WindowControls.svelte),
		// so the native frame goes; resizing, snapping and the shadow stay. On macOS
		// the traffic lights stay native and the header makes room for them.
		Frameless: goruntime.GOOS != "darwin",
		Windows: &windows.Options{
			Theme:                             windows.SystemDefault,
			DisableFramelessWindowDecorations: false,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About:    &mac.AboutInfo{Title: "TableF", Message: "Free and open source database client."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

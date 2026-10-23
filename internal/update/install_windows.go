package update

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Install takes TableF.exe out of the downloaded zip and swaps it in for the
// running one: Windows lets a running .exe be renamed, not overwritten, so the
// old one is moved aside (Cleanup removes it on the next start). It returns
// the executable to relaunch.
func Install(_ context.Context, zipPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("opening the update: %w", err)
	}
	defer zr.Close()
	var entry *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), "TableF.exe") {
			entry = f
		}
	}
	if entry == nil {
		return "", errors.New("the update has no TableF.exe")
	}
	next := exe + ".new"
	if err := extract(entry, next); err != nil {
		return "", fmt.Errorf("can't write next to %s (%w); download the update from the release page instead", exe, err)
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		return "", fmt.Errorf("can't replace %s: %w", exe, err)
	}
	if err := os.Rename(next, exe); err != nil {
		os.Rename(old, exe) // put the running version back
		return "", fmt.Errorf("installing the update: %w", err)
	}
	return exe, nil
}

func extract(f *zip.File, to string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		os.Remove(to)
		return err
	}
	return w.Close()
}

// Relaunch starts the new executable; the caller then quits.
func Relaunch(path string) error {
	return exec.Command(path).Start()
}

// Cleanup removes the executable an earlier update moved aside.
func Cleanup() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
		os.Remove(exe + ".new")
	}
}

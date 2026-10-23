package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// requirement accepts only apps signed by our Developer ID.
var requirement = fmt.Sprintf(`anchor apple generic and certificate leaf[subject.OU] = "%s"`, TeamID)

// Install unpacks the downloaded zip next to the running app, checks that it
// is signed by our Developer ID and notarized, then swaps it in place of the
// running bundle. It returns the bundle path to relaunch.
func Install(ctx context.Context, zipPath string) (string, error) {
	bundle, err := runningBundle()
	if err != nil {
		return "", err
	}
	return installInto(ctx, zipPath, bundle)
}

// installInto replaces the app bundle at path with the one in zipPath.
func installInto(ctx context.Context, zipPath, bundle string) (string, error) {
	// Unpack beside the app so the swap is a rename on the same volume.
	stage, err := os.MkdirTemp(filepath.Dir(bundle), ".tablef-update-")
	if err != nil {
		return "", fmt.Errorf("can't write next to %s (%w); download the update from the release page instead", bundle, err)
	}
	defer os.RemoveAll(stage)
	if out, err := exec.CommandContext(ctx, "ditto", "-x", "-k", zipPath, stage).CombinedOutput(); err != nil {
		return "", fmt.Errorf("unpacking the update: %v %s", err, out)
	}
	next := filepath.Join(stage, filepath.Base(bundle))
	if _, err := os.Stat(next); err != nil {
		next = filepath.Join(stage, "TableF.app")
	}
	if out, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", "-R="+requirement, next).CombinedOutput(); err != nil {
		return "", fmt.Errorf("the update isn't signed by TableF's developer, so it was not installed: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "spctl", "--assess", "--type", "execute", next).CombinedOutput(); err != nil {
		return "", fmt.Errorf("macOS doesn't accept the update (not notarized?), so it was not installed: %s", strings.TrimSpace(string(out)))
	}

	old := bundle + ".old"
	os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		return "", fmt.Errorf("can't replace %s (%w); download the update from the release page instead", bundle, err)
	}
	if err := os.Rename(next, bundle); err != nil {
		os.Rename(old, bundle) // put the running version back
		return "", fmt.Errorf("installing the update: %w", err)
	}
	os.RemoveAll(old) // the running process keeps its open files
	return bundle, nil
}

// runningBundle is the .app this process runs from.
func runningBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // TableF.app/Contents/MacOS/TableF
	if !strings.HasSuffix(bundle, ".app") {
		return "", errors.New("TableF isn't running from an app bundle; download the update from the release page instead")
	}
	return bundle, nil
}

// Relaunch starts the app at path once this process has quit.
func Relaunch(path string) error {
	return exec.Command("/bin/sh", "-c", `sleep 1; open "$0"`, path).Start()
}

// Cleanup removes leftovers of an earlier update (nothing on macOS).
func Cleanup() {}

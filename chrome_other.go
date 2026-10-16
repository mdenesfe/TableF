//go:build !darwin

package main

import "context"

// manageWindowChrome installs platform tweaks to the native window (none here).
func manageWindowChrome(context.Context) {}

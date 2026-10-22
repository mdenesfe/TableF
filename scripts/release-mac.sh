#!/usr/bin/env bash
# Builds the macOS app, signs it with a Developer ID (hardened runtime),
# notarizes it with Apple and staples the ticket, then packs it two ways:
#   build/bin/TableF-macos-universal.dmg the installer: a TableF-styled window to
#                                        drag the app to Applications (signed,
#                                        notarized and stapled too)
#   build/bin/TableF-macos-universal.zip the bare app
#
# Needs, on this Mac:
#   - a "Developer ID Application" identity in the login keychain
#   - notarytool credentials: xcrun notarytool store-credentials <profile>
#   - python3 with Pillow, and permission for the terminal to control
#     Finder (macOS asks once; see scripts/dmg/make-dmg.sh)
#
# Usage: scripts/release-mac.sh [--skip-build]
# Env:   TABLEF_SIGN_IDENTITY (default: the first Developer ID Application identity)
#        TABLEF_NOTARY_PROFILE (default: tablef)
set -euo pipefail

cd "$(dirname "$0")/.."
APP=build/bin/TableF.app
ZIP=build/bin/TableF-macos-universal.zip
DMG=build/bin/TableF-macos-universal.dmg
PROFILE=${TABLEF_NOTARY_PROFILE:-tablef}
IDENTITY=${TABLEF_SIGN_IDENTITY:-$(security find-identity -v -p codesigning | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)}

if [[ -z "$IDENTITY" ]]; then
	echo "No Developer ID Application identity in the keychain." >&2
	exit 1
fi

if [[ "${1:-}" != "--skip-build" ]]; then
	echo "==> Building"
	wails build -platform darwin/universal -trimpath -ldflags "-s -w" -skipbindings
fi

echo "==> Signing as $IDENTITY"
codesign --force --deep --options runtime --timestamp --sign "$IDENTITY" "$APP"
codesign --verify --strict --deep "$APP"

echo "==> Notarizing (profile $PROFILE); this takes a few minutes"
rm -f "$ZIP"
ditto -c -k --keepParent "$APP" "$ZIP"
xcrun notarytool submit "$ZIP" --keychain-profile "$PROFILE" --wait

echo "==> Stapling"
xcrun stapler staple "$APP"
xcrun stapler validate "$APP"
spctl --assess --type execute --verbose=2 "$APP"

# Zip again so the download carries the stapled ticket.
rm -f "$ZIP"
ditto -c -k --keepParent "$APP" "$ZIP"

echo "==> Building the installer"
scripts/dmg/make-dmg.sh "$APP" "$DMG"
codesign --force --timestamp --sign "$IDENTITY" "$DMG"

echo "==> Notarizing the installer"
xcrun notarytool submit "$DMG" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$DMG"
xcrun stapler validate "$DMG"
spctl --assess --type open --context context:primary-signature --verbose=2 "$DMG"
echo "==> Done: $DMG and $ZIP"

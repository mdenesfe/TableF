#!/usr/bin/env bash
# Packs an app into a TableF-styled DMG: the brand background with an arrow
# from the app to Applications. Finder itself lays out the window (through
# AppleScript), so the result shows correctly on every macOS version; tools
# that write the window settings by hand (dmgbuild) lose the background on
# macOS 26.
#
# Usage: scripts/dmg/make-dmg.sh <path/to/TableF.app> <out.dmg>
# Needs python3 with Pillow (for the background) and permission for the
# terminal to control Finder (macOS asks once).
set -euo pipefail

APP=$1
OUT=$2
VOL=TableF
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
trap 'hdiutil detach "/Volumes/$VOL" -quiet -force 2>/dev/null || true; rm -rf "$WORK"' EXIT

# A volume of the same name already mounted would take the window instead.
hdiutil detach "/Volumes/$VOL" -quiet -force 2>/dev/null || true

echo "    background"
python3 "$HERE/background.py" "$WORK/art" >/dev/null
mkdir -p "$WORK/stage/.background"
tiffutil -cathidpicheck "$WORK/art/background.png" "$WORK/art/background@2x.png" -out "$WORK/stage/.background/background.tiff" >/dev/null 2>&1
ditto "$APP" "$WORK/stage/$(basename "$APP")"
ln -s /Applications "$WORK/stage/Applications"

echo "    image"
hdiutil create -quiet -srcfolder "$WORK/stage" -volname "$VOL" -fs HFS+ -format UDRW -ov "$WORK/rw.dmg"
hdiutil attach -quiet -readwrite -noverify -noautoopen "$WORK/rw.dmg"

echo "    window layout"
# Bounds are the whole window (title bar included); content is 660x400 to
# match the background. Positions are icon centers (scripts/dmg/background.py).
osascript <<EOF
tell application "Finder"
	tell disk "$VOL"
		open
		set theWindow to container window
		set current view of theWindow to icon view
		set toolbar visible of theWindow to false
		set statusbar visible of theWindow to false
		set the bounds of theWindow to {200, 120, 860, 548}
		set opts to the icon view options of theWindow
		set arrangement of opts to not arranged
		set icon size of opts to 112
		set text size of opts to 13
		set background picture of opts to file ".background:background.tiff"
		set position of item "$(basename "$APP")" of theWindow to {180, 215}
		set position of item "Applications" of theWindow to {480, 215}
		update without registering applications
		delay 1
		close
	end tell
end tell
EOF
# Let Finder finish writing .DS_Store before detaching.
sleep 2
sync
hdiutil detach -quiet "/Volumes/$VOL"

echo "    compress"
rm -f "$OUT"
hdiutil convert -quiet "$WORK/rw.dmg" -format ULMO -o "$OUT"

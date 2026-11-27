#!/bin/sh
# Builds LooptenderMenu.app inside this package's .build directory, so the
# menu runs from an app bundle and can post notifications. It never installs,
# registers or opens the app: it only writes .build/LooptenderMenu.app, and
# .build/KeepgoingMenu.app as a link to it.
#
# The links under the app's earlier name (KeepgoingMenu.app beside it, and
# Contents/MacOS/KeepgoingMenu inside it) let a menu of an earlier build find
# this one when the install moves on, and keep a login item or a command that
# names the earlier path working. The bundle identifier is unchanged, so the
# notification permission and the login item stay. Remove the links once no
# menu of an earlier build runs.
# Remove it with: rm -rf integrations/macos/.build/LooptenderMenu.app integrations/macos/.build/KeepgoingMenu.app
set -eu
package=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# The menu's version, from the one place it is kept. It becomes the app's
# CFBundleShortVersionString, which the menu reports to the CLI.
menu_version=$(cat "$package/VERSION")
case "$menu_version" in
  *[!0-9.]* | "" | .* | *. | *..*) echo "VERSION must be a plain version such as 0.1.0, not '$menu_version'" >&2; exit 1 ;;
esac
# Source provenance belongs to the packaged app, not the mutable install link.
# Missing git metadata stays unknown; no paths or repository URLs are embedded.
source_revision=unknown
source_modified=false
if source_head=$(git -C "$package" rev-parse --verify HEAD 2>/dev/null); then
  case "$source_head" in
    *[!0-9a-f]* | "") ;;
    *) case ${#source_head} in 40|64) source_revision=$source_head ;; esac ;;
  esac
  if source_status=$(git -C "$package" status --porcelain --untracked-files=normal 2>/dev/null); then
    if [ -n "$source_status" ]; then source_modified=true; fi
  else
    source_revision=unknown
  fi
fi
swift build --package-path "$package" -c release
app="$package/.build/LooptenderMenu.app"
earlier="$package/.build/KeepgoingMenu.app"
rm -rf "$app" "$earlier"
mkdir -p "$app/Contents/MacOS"
cp "$package/.build/release/KeepgoingMenu" "$app/Contents/MacOS/LooptenderMenu"
ln -s LooptenderMenu "$app/Contents/MacOS/KeepgoingMenu"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>local.keepgoing.menu-preview</string>
	<key>CFBundleName</key>
	<string>looptender Menu</string>
	<key>CFBundleExecutable</key>
	<string>LooptenderMenu</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$menu_version</string>
	<key>LooptenderSourceRevision</key>
	<string>$source_revision</string>
	<key>LooptenderSourceModified</key>
	<$source_modified/>
	<key>CFBundleVersion</key>
	<string>0</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>LSUIElement</key>
	<true/>
</dict>
</plist>
PLIST
# An ad-hoc signature, for this computer only: macOS needs a signed bundle to
# keep its notification permission.
codesign --force --sign - "$app"
codesign --verify --deep --strict "$app"
ln -s LooptenderMenu.app "$earlier"
echo "Built $app"
echo "Open it with: $app/Contents/MacOS/LooptenderMenu --executable /path/to/looptender"

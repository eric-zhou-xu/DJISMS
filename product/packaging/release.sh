#!/bin/zsh
# Only run after explicit release authorization and Developer ID credentials.
set -eu
if (( $# != 3 )); then
  print -u2 'Usage: release.sh /path/DJISMS.app "Developer ID Application: …" notary-keychain-profile'
  exit 2
fi
APP="${1:A}"
IDENTITY="$2"
PROFILE="$3"
[[ "$APP" == */DJISMS.app && "$IDENTITY" == 'Developer ID Application:'* ]] || exit 2
/usr/bin/codesign --force --options runtime --timestamp --sign "$IDENTITY" "$APP/Contents/Helpers/djisms-core"
/usr/bin/codesign --force --options runtime --timestamp --sign "$IDENTITY" "$APP"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$APP"
ZIP="${APP:h}/DJISMS-notarization.zip"
/usr/bin/ditto -c -k --keepParent "$APP" "$ZIP"
/usr/bin/xcrun notarytool submit "$ZIP" --keychain-profile "$PROFILE" --wait
/usr/bin/xcrun stapler staple "$APP"
/usr/bin/xcrun stapler validate "$APP"
/usr/sbin/spctl --assess --type execute --verbose=2 "$APP"
/usr/bin/ditto -c -k --keepParent "$APP" "${APP:h}/DJISMS-release.zip"

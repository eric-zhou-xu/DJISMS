#!/bin/zsh
set -eu
ROOT="${0:A:h:h:h}"
GO="${DJISMS_GO:-go}"
VERSION="${DJISMS_VERSION:-1.0.0-devrc.1}"
BUNDLE_BUILD="${DJISMS_BUNDLE_BUILD:-1.0.8}"
cd "$ROOT"
SOURCE=$(/usr/bin/python3 product/packaging/manifest.py source "$ROOT")
BUILD_ID="${DJISMS_BUILD_ID:-$(/bin/date -u +%Y%m%dT%H%M%SZ)-${SOURCE[1,12]}}"
[[ "$VERSION" =~ '^[A-Za-z0-9._-]{1,80}$' && "$BUILD_ID" =~ '^[A-Za-z0-9._-]{1,80}$' && "$BUNDLE_BUILD" =~ '^[0-9]+\.[0-9]+\.[0-9]+$' ]] || exit 2
OUT="${DJISMS_BUILD_DIR:-$ROOT/build/djisms/$BUILD_ID}"
[[ ! -e "$OUT/DJISMS.app" ]] || { print -u2 'Refusing to overwrite an existing build; choose a new DJISMS_BUILD_DIR.'; exit 2; }
mkdir -p "$OUT/DJISMS.app/Contents/MacOS" "$OUT/DJISMS.app/Contents/Helpers" "$OUT/DJISMS.app/Contents/Resources"
INFO_PKG=github.com/iniwex5/vohive/product/djisms-core/buildinfo
CGO_CFLAGS="-mmacosx-version-min=14.0" CGO_LDFLAGS="-mmacosx-version-min=14.0" CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 MACOSX_DEPLOYMENT_TARGET=14.0 "$GO" build -trimpath -buildvcs=false -tags djisms_native -ldflags="-s -w -X $INFO_PKG.Version=$VERSION -X $INFO_PKG.BuildID=$BUILD_ID -X $INFO_PKG.SourceTree=$SOURCE" -o "$OUT/DJISMS.app/Contents/Helpers/djisms-core" ./product/djisms-core/cmd/djisms-core
/usr/bin/xcrun swiftc -swift-version 5 -O -target arm64-apple-macos14.0 -framework AppKit -framework UserNotifications -framework ServiceManagement product/DJISMS.app/Sources/*.swift -o "$OUT/DJISMS.app/Contents/MacOS/DJISMS"
cp product/packaging/Info.plist "$OUT/DJISMS.app/Contents/Info.plist"
cp product/DJISMS.app/Resources/* "$OUT/DJISMS.app/Contents/Resources/"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $BUNDLE_BUILD" "$OUT/DJISMS.app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Add :DJISMSVersion string $VERSION" -c "Add :DJISMSBuildID string $BUILD_ID" -c "Add :DJISMSSourceTree string $SOURCE" "$OUT/DJISMS.app/Contents/Info.plist"
/usr/bin/codesign --force --sign - --options runtime "$OUT/DJISMS.app/Contents/Helpers/djisms-core"
/usr/bin/codesign --force --sign - --options runtime "$OUT/DJISMS.app"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$OUT/DJISMS.app"
/usr/bin/plutil -lint "$OUT/DJISMS.app/Contents/Info.plist"
/usr/bin/file "$OUT/DJISMS.app/Contents/MacOS/DJISMS" "$OUT/DJISMS.app/Contents/Helpers/djisms-core"
/usr/bin/python3 product/packaging/manifest.py build "$ROOT" "$OUT/DJISMS.app" "$GO" "$SOURCE"

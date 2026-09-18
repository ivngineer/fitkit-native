#!/usr/bin/env bash
# Builds Fitkit, installs it on a paired iPhone and launches it.
#
#   ios/scripts/deploy-device.sh [device UDID or name]
#
# The device defaults to $FITKIT_DEVICE, then to the only paired iPhone.
# The app talks to the server on this Mac's LAN address, since localhost on
# the phone is the phone itself; set FITKIT_API_BASE_URL to override it.
set -euo pipefail

cd "$(dirname "$0")/.."

device="${1:-${FITKIT_DEVICE:-}}"
if [[ -z "$device" ]]; then
  devices=$(xcrun devicectl list devices --hide-headers 2>/dev/null \
    | grep -i physical | grep -i iphone | grep -oE '[0-9A-F]{8}-[0-9A-F]{16}' | sort -u)
  count=$(grep -c . <<<"$devices" || true)
  if [[ "$count" -ne 1 ]]; then
    echo "Found $count paired iPhones. Pass a UDID or set FITKIT_DEVICE." >&2
    xcrun devicectl list devices >&2
    exit 1
  fi
  device="$devices"
fi

if [[ -z "${FITKIT_API_BASE_URL:-}" ]]; then
  interface=$(route -n get default 2>/dev/null | awk '/interface:/ {print $2}')
  ip=$(ipconfig getifaddr "${interface:-en0}" || true)
  if [[ -z "$ip" ]]; then
    echo "Couldn't find this Mac's LAN address. Set FITKIT_API_BASE_URL." >&2
    exit 1
  fi
  FITKIT_API_BASE_URL="http://$ip:8080"
fi

derived="${FITKIT_DERIVED_DATA:-${TMPDIR:-/tmp}/fitkit-device-build}"
app="$derived/Build/Products/Debug-iphoneos/Fitkit.app"

echo "==> Building for $device against $FITKIT_API_BASE_URL"
# `$()` keeps Xcode from reading the `//` in the URL as a comment.
xcodebuild build -quiet \
  -project Fitkit.xcodeproj -scheme Fitkit \
  -destination "platform=iOS,id=$device" -allowProvisioningUpdates \
  -derivedDataPath "$derived" \
  FITKIT_API_BASE_URL="${FITKIT_API_BASE_URL/:\/\//:/\$()/}"

echo "==> Installing"
xcrun devicectl device install app --device "$device" "$app" >/dev/null

echo "==> Launching"
# A locked phone refuses the launch, but the install has already landed.
if ! xcrun devicectl device process launch --terminate-existing --device "$device" app.fitkit.Fitkit >/dev/null 2>&1; then
  echo "Installed, but couldn't launch it. The phone is probably locked, so open Fitkit there."
fi

if ! curl -fsS -m 3 "$FITKIT_API_BASE_URL/healthz" >/dev/null 2>&1; then
  echo "Note: nothing answers at $FITKIT_API_BASE_URL, so the app will open offline."
fi
echo "==> Done"

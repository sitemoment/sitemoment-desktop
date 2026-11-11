#!/usr/bin/env bash
# Signs SiteMoment.app / SiteMoment.dmg with a Developer ID (hardened runtime,
# build/darwin/entitlements.plist, secure timestamp) and notarizes and staples
# them, when the release has Apple credentials. Without MAC_SIGN_IDENTITY it
# changes nothing: the build's ad-hoc signature stays (an unsigned release,
# see RELEASING.md).
#
#   build/darwin/sign.sh app bin/SiteMoment.app
#   build/darwin/sign.sh dmg bin/SiteMoment.dmg
#
# Environment (all optional; the release workflow fills them from secrets):
#   MAC_SIGN_IDENTITY      "Developer ID Application: <Name> (<TEAMID>)", or "-" for a local
#                          ad-hoc test of the hardened-runtime signature (no timestamp, no notary)
#   MAC_SIGN_KEYCHAIN      keychain holding the identity (the workflow's temporary keychain)
#   MAC_NOTARY_KEY_FILE    App Store Connect API key (.p8) for notarytool,
#   MAC_NOTARY_KEY_ID      with its key ID
#   MAC_NOTARY_ISSUER_ID   and issuer ID; without all three the files are signed, not notarized
set -euo pipefail
kind=${1:-}
path=${2:-}
here=$(cd "$(dirname "$0")" && pwd)
if [ -z "$kind" ] || [ -z "$path" ]; then
  echo "usage: sign.sh app|dmg <path>" >&2
  exit 2
fi
if [ -z "${MAC_SIGN_IDENTITY:-}" ]; then
  echo "sign.sh: MAC_SIGN_IDENTITY is not set; $path keeps its ad-hoc signature (unsigned release)"
  exit 0
fi
id=$MAC_SIGN_IDENTITY
opts=(--force --sign "$id")
[ "$id" != "-" ] && opts+=(--timestamp)
[ -n "${MAC_SIGN_KEYCHAIN:-}" ] && opts+=(--keychain "$MAC_SIGN_KEYCHAIN")

# A partially-configured notary (e.g. a secret missing or misnamed in CI)
# must fail loudly, not silently fall back to "signed but not notarized".
notary_vars_set=0
for v in MAC_NOTARY_KEY_FILE MAC_NOTARY_KEY_ID MAC_NOTARY_ISSUER_ID; do
  [ -n "${!v:-}" ] && notary_vars_set=$((notary_vars_set + 1))
done
if [ "$notary_vars_set" -gt 0 ] && [ "$notary_vars_set" -lt 3 ]; then
  echo "sign.sh: MAC_NOTARY_KEY_FILE, MAC_NOTARY_KEY_ID and MAC_NOTARY_ISSUER_ID must be set together; $notary_vars_set of 3 are set" >&2
  exit 1
fi

notary_ready() {
  [ "$id" != "-" ] && [ -n "${MAC_NOTARY_KEY_FILE:-}" ] && [ -n "${MAC_NOTARY_KEY_ID:-}" ] && [ -n "${MAC_NOTARY_ISSUER_ID:-}" ]
}

# notarize submits $1 (a zip or a dmg), waits, and fails unless Apple accepts it.
notarize() {
  local auth=(--key "$MAC_NOTARY_KEY_FILE" --key-id "$MAC_NOTARY_KEY_ID" --issuer "$MAC_NOTARY_ISSUER_ID")
  local out status sub
  out=$(xcrun notarytool submit "$1" "${auth[@]}" --wait --timeout 30m --output-format json) || { echo "$out" >&2; exit 1; }
  status=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')
  sub=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
  echo "sign.sh: notarization $sub: $status"
  if [ "$status" != "Accepted" ]; then
    xcrun notarytool log "$sub" "${auth[@]}" >&2 || true
    exit 1
  fi
}

case "$kind" in
app)
  xattr -cr "$path"
  codesign "${opts[@]}" --options runtime --entitlements "$here/entitlements.plist" "$path"
  codesign --verify --strict --verbose=2 "$path"
  if notary_ready; then
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    ditto -c -k --keepParent "$path" "$tmp/notarize.zip"
    notarize "$tmp/notarize.zip"
    xcrun stapler staple "$path"
    xcrun stapler validate "$path"
    spctl --assess --type execute --verbose=2 "$path"
  else
    echo "sign.sh: notary key not set; $path is signed but not notarized"
  fi
  ;;
dmg)
  codesign "${opts[@]}" "$path"
  codesign --verify --verbose=2 "$path"
  if notary_ready; then
    notarize "$path"
    xcrun stapler staple "$path"
    xcrun stapler validate "$path"
    # context:primary-signature is the disk-image assessment (the "app"
    # branch's --type execute does not apply to a .dmg).
    spctl -a -t open --context context:primary-signature -v "$path"
  else
    echo "sign.sh: notary key not set; $path is signed but not notarized"
  fi
  ;;
*)
  echo "usage: sign.sh app|dmg <path>" >&2
  exit 2
  ;;
esac

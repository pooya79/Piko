#!/bin/sh
set -eu
# The reviewed SHA-384 digest pins the exact browser dependency bytes.
asset=static/htmx-4.0.0.min.js
expected=BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc
curl -fsSL https://unpkg.com/htmx.org@4.0.0/dist/htmx.min.js -o "$asset"
actual=$(openssl dgst -sha384 -binary "$asset" | openssl base64 -A)
if [ "$actual" != "$expected" ]; then
  rm -f "$asset"
  echo "HTMX digest mismatch" >&2
  exit 1
fi

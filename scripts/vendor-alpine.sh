#!/bin/sh
set -eu
# The reviewed SHA-384 digest pins the exact CSP-compatible browser dependency bytes.
asset=static/alpine-csp-3.17.4.min.js
expected=DQd2BgbtOQrdt/JbxcM+wSb8poOHwsG5W2jbbXskj85r7SWPwqpBFgGGR1devM/D
curl -fsSL https://unpkg.com/@alpinejs/csp@3.17.4/dist/cdn.min.js -o "$asset"
actual=$(openssl dgst -sha384 -binary "$asset" | openssl base64 -A)
if [ "$actual" != "$expected" ]; then
  rm -f "$asset"
  echo "Alpine CSP digest mismatch" >&2
  exit 1
fi

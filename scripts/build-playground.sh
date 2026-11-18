#!/usr/bin/env bash
# build-playground.sh — rebuild the zero-install browser playground.
# Produces playground/jseek.wasm and refreshes wasm_exec.js from the active Go
# toolchain, so the page always matches the engine it loads.
# Usage: ./scripts/build-playground.sh
set -euo pipefail
cd "$(dirname "$0")/.."

echo ">>> building jseek.wasm (GOOS=js GOARCH=wasm)"
GOOS=js GOARCH=wasm go build -o playground/jseek.wasm ./playground/wasm

echo ">>> refreshing wasm_exec.js from $(go env GOROOT)"
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" playground/wasm_exec.js 2>/dev/null \
  || cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" playground/wasm_exec.js

echo ">>> done. Serve the playground locally:"
echo "    cd playground && python3 -m http.server 8080"
echo "    then open http://localhost:8080"
echo "    (WASM requires http(s); opening index.html via file:// will not load)"
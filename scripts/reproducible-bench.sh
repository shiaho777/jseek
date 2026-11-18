#!/usr/bin/env bash
# reproducible-bench.sh — one command to reproduce every headline number.
# Requires: go 1.23+, and (for the CLI comparison) jq on PATH.
# Usage: ./scripts/reproducible-bench.sh
set -euo pipefail
cd "$(dirname "$0")/.."

echo "============================================================"
echo " jseek reproducible benchmarks"
echo " go: $(go version)"
echo "============================================================"

echo
echo ">>> [1/4] Library: lazy extraction vs gjson/jsonparser (bench module)"
( cd bench && go test -run '^$' \
    -bench 'BenchmarkSmall_|BenchmarkLargeShallow_|BenchmarkLargeIndexed_|BenchmarkManyFields_' \
    -benchmem -benchtime=300ms -count=3 ./... )

echo
echo ">>> [2/4] Library: index-once / tape / transpose (repeated access)"
( cd bench && go test -run '^$' \
    -bench 'BenchmarkTapeAB_|BenchmarkColumnar_|BenchmarkIndexBuild_' \
    -benchmem -benchtime=300ms -count=3 ./... )

echo
echo ">>> [3/4] Substrate: zero-parse open + WAL cheap writes + subtree reads"
go test -run '^$' \
  -bench 'BenchmarkOpenVsIndex|BenchmarkWALWrite|BenchmarkWALDirtyRead|BenchmarkNDJSONRandomAccess|BenchmarkPatchVsRebuild' \
  -benchmem -benchtime=300ms -count=3 .

echo
echo ">>> [4/4] CLI vs jq (real files)"
if command -v jq >/dev/null 2>&1; then
  go build -o /tmp/jseek-bench ./cmd/jseek
  python3 - <<'PY'
import json
users=[{"id":i,"name":"u%d"%i,"age":20+i%50,"active":i%3==0} for i in range(50000)]
json.dump({"users":users},open("/tmp/jseek-bench.json","w"),separators=(",",":"))
with open("/tmp/jseek-bench.ndjson","w") as f:
    for i in range(100000):
        f.write(json.dumps({"id":i,"latency_ms":i%500},separators=(",",":"))+"\n")
print("generated /tmp/jseek-bench.json (50k users) and .ndjson (100k lines)")
PY
  echo "--- single field (50k-user doc) ---"
  echo "jseek:"; /usr/bin/time -p /tmp/jseek-bench 'users[49999].name' /tmp/jseek-bench.json >/dev/null
  echo "jq:";    /usr/bin/time -p jq '.users[49999].name' /tmp/jseek-bench.json >/dev/null
  echo "--- NDJSON field (100k lines) ---"
  echo "jseek:"; /usr/bin/time -p /tmp/jseek-bench -n latency_ms /tmp/jseek-bench.ndjson >/dev/null
  echo "jq:";    /usr/bin/time -p jq .latency_ms /tmp/jseek-bench.ndjson >/dev/null
else
  echo "jq not found — skipping CLI comparison (install jq to run it)"
fi

echo
echo "============================================================"
echo " done. Numbers above are reproducible on this machine."
echo " See docs/BENCHMARKS.md for methodology and recorded boundaries."
echo "============================================================"
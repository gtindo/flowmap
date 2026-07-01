#!/usr/bin/env bash

set -euo pipefail

readonly flowmap_binary="${1:?usage: compatibility-smoke.sh <flowmap-binary> <module-path>}"
readonly module_path="${2:?usage: compatibility-smoke.sh <flowmap-binary> <module-path>}"
readonly output_file="$(mktemp)"
flowmap_pid=""

cleanup() {
  if [[ -n "$flowmap_pid" ]] && kill -0 "$flowmap_pid" 2>/dev/null; then
    kill "$flowmap_pid" 2>/dev/null || true
    wait "$flowmap_pid" 2>/dev/null || true
  fi
  rm -f "$output_file"
}
trap cleanup EXIT

"$flowmap_binary" serve "$module_path" --addr 127.0.0.1:0 >"$output_file" 2>&1 &
flowmap_pid=$!

for _ in {1..100}; do
  if grep -q "Flowmap indexed" "$output_file"; then
    cat "$output_file"
    exit 0
  fi
  if ! kill -0 "$flowmap_pid" 2>/dev/null; then
    cat "$output_file" >&2
    exit 1
  fi
  sleep 0.1
done

cat "$output_file" >&2
echo "compatibility smoke test: Flowmap did not finish indexing" >&2
exit 1

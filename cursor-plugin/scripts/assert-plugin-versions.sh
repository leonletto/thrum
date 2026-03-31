#!/usr/bin/env bash
set -euo pipefail

root_version="$(python3 - <<'PY'
import json
print(json.load(open('claude-plugin/.claude-plugin/plugin.json'))['version'])
PY
)"
cursor_version="$(python3 - <<'PY'
import json
print(json.load(open('cursor-plugin/.cursor-plugin/plugin.json'))['version'])
PY
)"
market_version="$(python3 - <<'PY'
import json
print(json.load(open('.cursor-plugin/marketplace.json'))['plugins'][0]['version'])
PY
)"

test "${root_version}" = "${cursor_version}"
test "${root_version}" = "${market_version}"
echo "plugin-version-sync-ok ${root_version}"

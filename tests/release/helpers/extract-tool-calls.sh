#!/usr/bin/env bash
# tests/release/helpers/extract-tool-calls.sh — extract tool-use entries
# from an AI runtime's session transcript. v1 supports claude only.
#
# Sourceable: defines the function `extract_tool_calls`. Also runnable
# directly: `extract-tool-calls.sh <runtime> <session_dir>` prints the
# tool-call array to stdout.

extract_tool_calls() {
  local runtime="$1" session_dir="${2:-}"
  case "$runtime" in
    claude)
      # Each session under ~/.claude/projects/<id>/<session>/<file>.jsonl
      # has tool_use entries. Project them down.
      if [[ -z "$session_dir" || ! -d "$session_dir" ]]; then echo "[]"; return 0; fi
      find "$session_dir" -name '*.jsonl' -print0 \
        | xargs -0 cat 2>/dev/null \
        | jq -s '[.[]? | select(.type=="tool_use")? | {tool: .name, args_summary: ((.input // {}) | tostring | .[0:120])}]' \
        2>/dev/null || echo "[]"
      ;;
    *)
      echo "[]"   # other runtimes not yet supported in v1
      ;;
  esac
}

# Allow direct invocation as a script for ad-hoc use.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  extract_tool_calls "${1:-claude}" "${2:-${HOME}/.claude/projects}"
fi

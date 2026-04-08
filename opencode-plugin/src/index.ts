import type { Plugin, PluginModule } from "@opencode-ai/plugin"
import { installAssets } from "./installer.js"
import { shellEnvHook } from "./hooks/shell-env.js"
import { blockSyncCdHook } from "./hooks/block-sync-cd.js"

const TOOL_MAPPING = `
## Thrum Plugin — Tool Mapping for OpenCode

When Thrum skills reference Claude Code tool names, use these OpenCode equivalents:
- TodoWrite → todowrite
- Task (subagents) → @mention syntax (e.g., @explore, @general)
- Skill tool → OpenCode's native skill tool
- Agent tool → @mention syntax for subagents
- Read, Write, Edit, Bash → your native tools (same names, lowercase)
`.trim()

const server: Plugin = async (ctx) => {
  await installAssets(ctx)

  return {
    "shell.env": shellEnvHook(ctx),
    "tool.execute.before": blockSyncCdHook(),
    "experimental.chat.system.transform": async (
      _input: { sessionID?: string; model?: unknown },
      output: { system: string[] },
    ) => {
      ;(output.system ||= []).push(TOOL_MAPPING)
    },
  }
}

export default { id: "opencode-thrum", server } satisfies PluginModule

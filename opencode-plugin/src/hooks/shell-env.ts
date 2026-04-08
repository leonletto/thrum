import fs from "fs"
import path from "path"
import type { PluginInput } from "@opencode-ai/plugin"

export function shellEnvHook(ctx: PluginInput) {
  return async (
    input: { cwd: string; sessionID?: string; callID?: string },
    output: { env: Record<string, string> },
  ) => {
    const dirs = [input.cwd, ctx.directory, ctx.worktree].filter(Boolean)

    for (const dir of dirs) {
      const identityPath = path.join(dir, ".thrum", "identity.json")
      try {
        const raw = fs.readFileSync(identityPath, "utf8")
        const identity = JSON.parse(raw)
        output.env.THRUM_NAME = identity.agent_id ?? ""
        output.env.THRUM_ROLE = identity.role ?? ""
        output.env.THRUM_MODULE = identity.module ?? ""
        output.env.THRUM_HOME = dir
        return
      } catch {
        // No identity file in this dir, try next
      }
    }
  }
}

export function blockSyncCdHook() {
  return async (
    input: { tool: string; sessionID: string; callID: string },
    output: { args: Record<string, any> },
  ) => {
    if (input.tool === "bash") {
      const cmd = String(output.args?.command ?? "")
      if (/\bcd\b.*\.thrum[/\\]sync/.test(cmd)) {
        throw new Error(
          "Blocked: do not cd into .thrum/sync worktree — this is the git sync branch and modifying it directly will corrupt sync state.",
        )
      }
    }
  }
}

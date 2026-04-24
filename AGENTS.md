<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->

## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full
workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown
  TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT
complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs
   follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:

   ```bash
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```

   (No `bd dolt push` — this project uses bd in embedded mode; beads backups are
   handled by `dev-docs/dev-scripts/sync-dev-docs.sh`.)

5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**

- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->

## Project-Specific Push Policy (overrides generic guidance above)

The beads integration block above contains generic "PUSH TO REMOTE - This is
MANDATORY: git push" guidance from the bd template. **For this repo, that
mandate applies ONLY when you're on `thrum-dev` in the main repo.** It does NOT
mean push whatever branch you happen to be on.

### Branch push rules

| Branch | Push to origin | Why |
| --- | --- | --- |
| `thrum-dev` | Every session end | Authoritative pre-release truth; protects work |
| `feature/*` / `fix/*` | NEVER auto-push | Local-only by design; reach origin via merge into `thrum-dev` |
| `website-dev` | Only when ready to deploy | Push triggers website deployment workflow (`deploy-pages.yml`) |
| `main` | Only via release flow | See `dev-docs/RELEASE-STEPS.md` |

Feature branches in worktrees are many, temporary, and frequently reused for new
tasks. Long-running implementer worktrees (e.g. `team-fix`) often switch what
they're working on across sessions and accumulate experimental commits. Pushing
those would clutter the public repo with intermediate states that may never
land. Code reaches origin by being merged into `thrum-dev` first.

A bare `git push` from inside a feature worktree would push that feature branch
to origin — which is wrong per this project's policy. If you're unsure which
branch you're on, run `git status` first and consult the table before pushing.

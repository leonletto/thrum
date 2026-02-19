# Thrum Skill Maintenance

## Structure

```
claude-plugin/
├── .claude-plugin/
│   ├── plugin.json           # Hooks (SessionStart, PreCompact)
│   └── marketplace.json      # Local plugin discovery
├── skills/thrum/
│   ├── SKILL.md              # Entry point (~135 lines, hybrid format)
│   ├── CLAUDE.md             # This file
│   └── resources/            # Progressive disclosure (8 files)
└── commands/                 # Slash commands (10 files)
```

## Conventions

- **SKILL.md** stays under 150 lines. Dense command reference first, then
  decision table, protocol, resource links.
- **Resources** stay under 1,500 words each. Task-oriented naming
  (LISTENER_PATTERN.md not LISTENER.md).
- **Commands** stay under 100 words each. Frontmatter: `description`, optional
  `argument-hint`.
- **allowed-tools:** `Bash(thrum:*)` only — no `Read`. All data via CLI output.
- **DRY:** Never duplicate CLI docs. Point to `thrum prime` and
  `thrum <cmd> --help`.
- **Version** in SKILL.md frontmatter and plugin.json must match.

## Updating

When adding new thrum CLI commands:

1. Add to Quick Command Reference in SKILL.md (if commonly used)
2. Add to CLI_REFERENCE.md (always)
3. Add slash command in `commands/` if it benefits from guided execution
4. Update version in both SKILL.md and plugin.json

When changing CLI flag syntax:

1. Update CLI_REFERENCE.md
2. Check SKILL.md command reference for affected commands
3. Check resource files for inline examples

## Testing

Plugin hooks and slash commands are verified manually:

- `thrum prime` — verify output includes all sections
- `/thrum:send` — verify guided send works
- Start new session — verify SessionStart hook runs `thrum prime`

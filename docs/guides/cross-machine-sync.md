## Sync Across Machines

Thrum messages live on a Git branch. That means sync across machines is just Git
push and pull — no cloud service, no account, no special infrastructure.

This walkthrough takes you from two separate clones to agents on different
machines exchanging messages.

### What You Need

- The same git repo cloned on two machines (or two separate paths on one machine
  for testing)
- A shared remote configured on both (`git remote -v` to verify)
- Thrum initialized: `thrum init` done on both clones
- Thrum daemon running on both (`thrum init` starts it automatically)

### Enable Sync

By default, Thrum runs in `local_only` mode — it doesn't push or pull from the
remote. Turn that off.

In your config file (`.thrum/config.yaml` in the repo root):

```yaml
local_only: false
```

Or set it via environment variable without touching the file:

```bash
export THRUM_LOCAL_ONLY=false
```

Do this on both machines. The daemon picks up the config on next start, or
restart it now:

```bash
thrum daemon restart
```

### Send on Machine A

Register an agent and send a message:

```bash
thrum quickstart --role implementer --module api --intent "Working on API changes"
thrum send "Deploy script updated, ready to test on staging"
```

Then push the sync branch manually to make it available immediately:

```bash
thrum sync force
```

Without `thrum sync force`, the daemon syncs automatically every 60 seconds.
Force sync is useful when you want the other machine to see something right now.

### Receive on Machine B

On the second machine, pull the latest sync branch and check for messages:

```bash
thrum sync force
thrum inbox --unread
```

The message from Machine A appears. The sync works through the `a-sync` orphan
branch in your repo — run `git log origin/a-sync` to see exactly what came in.

### Automatic Sync

You don't have to run `thrum sync force` manually. The daemon runs a sync loop
every 60 seconds when `local_only` is false.

Check that it's running:

```bash
thrum sync status
```

You'll see the last sync time, whether it succeeded, and which remote it's
using. If sync is failing, the error shows up here.

### Verify with Git

Thrum sync is transparent. You can inspect it directly:

```bash
# See what's on the sync branch
git log --oneline origin/a-sync -10

# See message files
git show origin/a-sync:messages/
```

Nothing is hidden. The sync branch is just a regular orphan branch in your repo.

### Optional: Tailscale for Real-Time Sync

Git-based sync is great for async workflows — messages arrive within 60 seconds.
If you need real-time propagation (sub-second delivery across machines),
Tailscale sync is the answer.

Tailscale connects your machines over an encrypted WireGuard tunnel and lets
Thrum daemons push events directly to each other instead of going through Git.

See [Tailscale Sync](../tailscale-sync.md) for setup. It's a few steps to pair
the machines, then everything is real-time automatically.

### Troubleshooting

**Messages not appearing on the other machine?**

1. Check sync is enabled: `thrum sync status` — look for `local_only: false`
2. Verify the remote is reachable: `git fetch origin` — any errors here will
   block sync
3. Confirm the `a-sync` branch exists on the remote:
   `git ls-remote origin a-sync`
4. Check the daemon is running on both sides: `thrum daemon status`
5. Try a manual sync: `thrum sync force` and watch for errors

**"local_only mode" in sync status?** Set `local_only: false` in config or via
`THRUM_LOCAL_ONLY=false` and restart the daemon.

**Messages show up on one machine but not the other?** Both machines need to be
pushing. Check `thrum sync status` on both — if one shows a sync error, fix it
there.

### Next Steps

- [Sync Protocol](../sync.md) — architecture detail on how the `a-sync` branch
  works, JSONL dedup, and conflict-free merging
- [Tailscale Sync](../tailscale-sync.md) — real-time cross-machine sync without
  polling
- [Configuration](../configuration.md) — full config reference including
  `local_only`, sync interval, and remote settings

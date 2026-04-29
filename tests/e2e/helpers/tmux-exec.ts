/**
 * tmux-based command execution for E2E tests.
 *
 * Runs CLI commands inside an isolated tmux session to break the PID
 * ancestry chain. This prevents thrum's PID-based identity resolution
 * from finding the developer's Claude process and adopting the wrong
 * agent identity.
 *
 * Pattern: ONE persistent named session per test process (lazily
 * created on first tmuxExec call, killed by tmuxKillServer at
 * teardown). Each command is sent via send-keys with a unique
 * done-marker; output is redirected to a per-call /tmp file (not
 * scrollback). Polling matches `^<MARKER>:<exit-code>$` in
 * capture-pane; output is read from the /tmp file.
 *
 * Why not respawn-pane (the previous design): each respawn-pane call
 * allocates a fresh PTY, and macOS's PTY pool exhausts after ~30-50
 * calls in a single process — fork() returns ENXIO ("Device not
 * configured") and the suite hangs. The persistent-session pattern
 * uses a single PTY for the whole run, eliminating churn. Reference
 * implementation: tests/release/helpers/drive.sh + scripts/tmux-exec
 * shell-run, both proven across the 96-scenario release-test suite.
 *
 * Env isolation: each command is wrapped in a subshell `(cd <cwd> &&
 * <cmd>)`; the cmd string is built upstream by helpers/thrum-cli.ts
 * with a `THRUM_*=...` prefix from buildEnvPrefix, so identity vars
 * scope to that command only. The persistent shell is launched with
 * `bash --noprofile --norc` and a clean inherited env so no developer
 * shell state leaks in.
 */
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

/** Unique tmux socket per test run — isolates from user's tmux. */
export const TMUX_SOCKET = `thrum-e2e-${process.pid}`;

/** Low-level tmux command runner. All tmux calls go through here. */
export function tmuxRun(...args: string[]): string {
  return execFileSync('tmux', ['-u', '-L', TMUX_SOCKET, ...args], {
    encoding: 'utf-8',
    timeout: 10_000,
  }).trim();
}

export interface TmuxExecResult {
  stdout: string;
  exitCode: number;
}

/**
 * Strip a single trailing newline. Output captured from the per-call
 * /tmp file is the raw bytes the command wrote to its stdout/stderr;
 * we trim only the final \n so callers that .trim() get the same
 * shape as the previous respawn-pane impl.
 */
function trimTrailingNewline(s: string): string {
  return s.endsWith('\n') ? s.slice(0, -1) : s;
}

// ── Persistent-session state ────────────────────────────────────────────────

let persistentSession: string | null = null;
let persistentTmpDir: string | null = null;

function persistentTmpRoot(): string {
  if (persistentTmpDir) return persistentTmpDir;
  persistentTmpDir = mkdtempSync(join(tmpdir(), `thrum-e2e-${process.pid}-`));
  return persistentTmpDir;
}

/**
 * Lazily create the per-process persistent session. Spawns
 * `bash --noprofile --norc` so prompts/aliases/RC files don't
 * contaminate scrollback or marker matching. -x/-y widen the
 * virtual terminal so command echoes don't physically wrap.
 */
function ensurePersistentSession(): string {
  if (persistentSession) return persistentSession;
  const name = `e2e-pool-${process.pid}-${Date.now().toString(36)}`;
  tmuxRun(
    'new-session', '-d', '-s', name, '-x', '500', '-y', '50',
    'bash', '--noprofile', '--norc',
  );
  // Settle PS1 to a known minimal form so the prompt doesn't share a
  // line with our marker echo (would defeat ^MARKER:N$ regex). Send
  // both PS1 and PROMPT_COMMAND wipes; an Enter to submit.
  tmuxRun('send-keys', '-t', name, `PS1=''; PROMPT_COMMAND=''`, 'Enter');
  persistentSession = name;
  return name;
}

/**
 * Send a single command into the persistent session and wait for its
 * done-marker to land in scrollback. Output goes to a per-call /tmp
 * file (one open()/close() per call, not per scrollback line).
 *
 * Marker pattern: `<cmd> > <outfile> 2>&1; echo "<MARKER>:$?"` —
 * `$?` captures <cmd>'s exit code (the redirect doesn't change it),
 * the marker is unique-per-call (uuid) so prior-call markers in
 * scrollback never false-match.
 */
function sendAndWait(
  session: string,
  cmd: string,
  cwd: string | undefined,
  timeoutMs: number,
): TmuxExecResult {
  const marker = `__E2E_DONE_${randomUUID().replace(/-/g, '')}__`;
  const outFile = join(persistentTmpRoot(), `out-${marker}.txt`);
  // Pre-create the file so a parse failure path before the command
  // runs still has something to read (avoids ENOENT noise).
  writeFileSync(outFile, '');

  const cwdPart = cwd ? `cd ${shellEscape(cwd)} && ` : '';
  const line = `${cwdPart}(${cmd}) > ${shellEscape(outFile)} 2>&1; echo "${marker}:$?"`;
  tmuxRun('send-keys', '-t', session, line, 'Enter');

  const deadline = Date.now() + timeoutMs;
  let rc: number | null = null;
  const markerRe = new RegExp(`^${marker}:(\\d+)$`, 'm');
  while (Date.now() < deadline) {
    const pane = tmuxRun('capture-pane', '-p', '-J', '-t', session, '-S', '-');
    const m = pane.match(markerRe);
    if (m) {
      rc = parseInt(m[1], 10);
      break;
    }
    execFileSync('sleep', ['0.05']); // 50ms
  }

  let stdout = '';
  try {
    stdout = trimTrailingNewline(readFileSync(outFile, 'utf-8'));
  } catch {
    /* file unreadable — leave stdout empty */
  }
  try { unlinkSync(outFile); } catch { /* best effort */ }

  if (rc === null) {
    throw new Error(
      `tmux exec timed out after ${timeoutMs}ms. Partial output:\n${stdout}`,
    );
  }
  return { stdout, exitCode: rc };
}

/**
 * Execute a shell command inside the persistent isolated tmux
 * session. Returns stdout and exit code. Synchronous.
 *
 * Public API preserved: callers (thrum-cli.ts, spec files) need no
 * edits. The cmd string is run as `(cd <cwd> && <cmd>)` so any
 * env-prefix the caller embedded (THRUM_NAME=... thrum ...) scopes
 * to a subshell.
 */
export function tmuxExec(
  cmd: string,
  opts?: { cwd?: string; timeoutMs?: number },
): TmuxExecResult {
  const session = ensurePersistentSession();
  const timeoutMs = opts?.timeoutMs ?? 30_000;
  return sendAndWait(session, cmd, opts?.cwd, timeoutMs);
}

/**
 * Async variant — same persistent session, polled via setInterval so
 * the caller's event loop stays free. Used for commands like `thrum
 * wait` that must run concurrently with other actions.
 *
 * Note on concurrency: the persistent shell processes commands
 * sequentially, so two `tmuxExecAsync` calls in flight at once would
 * serialize. Today no caller actually fires two concurrently (only
 * one async caller exists, and it's awaited). If a future test needs
 * true parallelism, dispatch each parallel command into its own
 * named session via tmuxCreateSession + tmuxSendKeys.
 */
export function tmuxExecAsync(
  cmd: string,
  opts?: { cwd?: string; timeoutMs?: number },
): Promise<TmuxExecResult> {
  const session = ensurePersistentSession();
  const timeoutMs = opts?.timeoutMs ?? 30_000;
  const marker = `__E2E_DONE_${randomUUID().replace(/-/g, '')}__`;
  const outFile = join(persistentTmpRoot(), `out-${marker}.txt`);
  writeFileSync(outFile, '');

  const cwdPart = opts?.cwd ? `cd ${shellEscape(opts.cwd)} && ` : '';
  const line = `${cwdPart}(${cmd}) > ${shellEscape(outFile)} 2>&1; echo "${marker}:$?"`;
  tmuxRun('send-keys', '-t', session, line, 'Enter');

  const start = Date.now();
  const markerRe = new RegExp(`^${marker}:(\\d+)$`, 'm');

  return new Promise((resolve) => {
    const poll = setInterval(() => {
      try {
        const pane = tmuxRun(
          'capture-pane', '-p', '-J', '-t', session, '-S', '-',
        );
        const m = pane.match(markerRe);
        const elapsed = Date.now() - start;
        if (m || elapsed > timeoutMs) {
          clearInterval(poll);
          let stdout = '';
          try { stdout = trimTrailingNewline(readFileSync(outFile, 'utf-8')); } catch { /* empty */ }
          try { unlinkSync(outFile); } catch { /* best effort */ }
          const exitCode = m ? parseInt(m[1], 10) : 1;
          resolve({ stdout, exitCode });
        }
      } catch {
        // Server may have been killed externally
        clearInterval(poll);
        resolve({ stdout: '', exitCode: 1 });
      }
    }, 200);
  });
}

/**
 * Kill the test tmux server. Call during setup to clean stale
 * servers and during teardown to release the persistent session +
 * its scratch tmpdir.
 */
export function tmuxKillServer(): void {
  try {
    tmuxRun('kill-server');
  } catch {
    /* no server running — expected */
  }
  persistentSession = null;
  // Per-call /tmp files are unlinked inline; the parent dir is left
  // for inspection on failure (matches global-teardown's "preserve
  // artifacts" policy). It's auto-purged by the OS at next reboot.
  persistentTmpDir = null;
}

// --- Persistent session helpers (for Steps 7-9 Claude Code tests) ---
//
// These are unrelated to the per-process exec pool above — they
// create separately-named sessions for tests that drive Claude Code
// panes directly. Untouched by the x6e8.5 migration.

/** Create a persistent tmux session with a shell. */
export function tmuxCreateSession(
  name: string,
  cwd: string,
  size?: { x: number; y: number },
): void {
  const args = ['new-session', '-d', '-s', name, '-c', cwd];
  if (size) {
    args.push('-x', String(size.x), '-y', String(size.y));
  }
  tmuxRun(...args);
}

/** Send keystrokes to a persistent tmux session. */
export function tmuxSendKeys(session: string, keys: string): void {
  tmuxRun('send-keys', '-t', session, keys);
}

/** Capture pane output from a persistent tmux session. */
export function tmuxCapture(session: string, lines?: number): string {
  if (lines) {
    return tmuxRun('capture-pane', '-p', '-t', session, '-S', `-${lines}`);
  }
  return tmuxRun('capture-pane', '-p', '-t', session, '-S', '-', '-E', '-');
}

/** Kill a persistent tmux session. */
export function tmuxKillSession(session: string): void {
  try {
    tmuxRun('kill-session', '-t', session);
  } catch {
    /* session may already be dead */
  }
}

// --- Shell escaping ---

/** Escape a string for safe use in sh -c '...' */
export function shellEscape(s: string): string {
  // Wrap in single quotes and escape internal single quotes
  return "'" + s.replace(/'/g, "'\\''") + "'";
}

/**
 * Build a shell env prefix from an env object.
 * Extracts only THRUM_* keys and formats as inline shell assignments.
 */
export function buildEnvPrefix(env?: NodeJS.ProcessEnv): string {
  if (!env) return '';
  const thrumKeys = Object.keys(env).filter(k => k.startsWith('THRUM_'));
  if (thrumKeys.length === 0) return '';
  return thrumKeys
    .map(k => `${k}=${shellEscape(env[k] ?? '')}`)
    .join(' ');
}

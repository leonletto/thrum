/**
 * tmux-based command execution for E2E tests.
 *
 * Runs CLI commands inside isolated tmux sessions to break the PID ancestry
 * chain. This prevents thrum's PID-based identity resolution from finding the
 * developer's Claude process and adopting the wrong agent identity.
 *
 * Pattern borrowed from Gastown: remain-on-exit + pane_dead polling.
 */
import { execFileSync } from 'node:child_process';

/** Unique tmux socket per test run — isolates from user's tmux. */
export const TMUX_SOCKET = `thrum-e2e-${process.pid}`;

/** Low-level tmux command runner. All tmux calls go through here. */
export function tmuxRun(...args: string[]): string {
  return execFileSync('tmux', ['-u', '-L', TMUX_SOCKET, ...args], {
    encoding: 'utf-8',
    timeout: 10_000,
  }).trim();
}

/**
 * Strip tmux pane artifacts from captured output:
 * - "Pane is dead (status N, ...)" footer
 * - Trailing blank lines
 */
function cleanOutput(raw: string): string {
  const lines = raw.split('\n');
  // Remove "Pane is dead" footer (last non-blank line when present)
  const cleaned = lines.filter(line => !line.startsWith('Pane is dead'));
  // Trim trailing blank lines
  while (cleaned.length > 0 && cleaned[cleaned.length - 1].trim() === '') {
    cleaned.pop();
  }
  return cleaned.join('\n');
}

/** Unique session counter to avoid collisions. */
let sessionCounter = 0;

export interface TmuxExecResult {
  stdout: string;
  exitCode: number;
}

/**
 * Execute a shell command inside an isolated tmux session.
 * Returns stdout and exit code. Fully synchronous.
 *
 * The command runs in a fresh tmux pane with no Claude process ancestry,
 * so PID-based identity resolution will not fire.
 */
export function tmuxExec(
  cmd: string,
  opts?: { cwd?: string; timeoutMs?: number },
): TmuxExecResult {
  const timeoutMs = opts?.timeoutMs ?? 30_000;
  const session = `exec-${++sessionCounter}-${Date.now().toString(36)}`;

  try {
    // Create session with clean shell
    const newArgs = ['new-session', '-d', '-s', session];
    if (opts?.cwd) newArgs.push('-c', opts.cwd);
    tmuxRun(...newArgs);

    // Enable death tracking so pane_dead and pane_dead_status are available
    tmuxRun('set-option', '-t', session, 'remain-on-exit', 'on');

    // Replace shell with the actual command
    tmuxRun('respawn-pane', '-k', '-t', session, '--', 'sh', '-c', cmd);

    // Poll for completion
    const deadline = Date.now() + timeoutMs;
    let timedOut = true;
    while (Date.now() < deadline) {
      const dead = tmuxRun('display-message', '-p', '-t', session, '#{pane_dead}');
      if (dead === '1') {
        timedOut = false;
        break;
      }
      execFileSync('sleep', ['0.05']); // 50ms
    }

    // Read exit code and output
    const exitCodeStr = tmuxRun(
      'display-message', '-p', '-t', session, '#{pane_dead_status}',
    );
    const rawOutput = tmuxRun('capture-pane', '-p', '-t', session, '-S', '-');
    const stdout = cleanOutput(rawOutput);
    const exitCode = parseInt(exitCodeStr, 10) || 0;

    if (timedOut) {
      throw new Error(
        `tmux exec timed out after ${timeoutMs}ms. Partial output:\n${stdout}`,
      );
    }

    return { stdout, exitCode };
  } finally {
    try {
      tmuxRun('kill-session', '-t', session);
    } catch {
      /* session may already be dead */
    }
  }
}

/**
 * Start an async tmux exec that runs in the background.
 * Returns a Promise that resolves when the command exits.
 * Used for commands like `thrum wait` that must run concurrently with other actions.
 */
export function tmuxExecAsync(
  cmd: string,
  opts?: { cwd?: string; timeoutMs?: number },
): Promise<TmuxExecResult> {
  const timeoutMs = opts?.timeoutMs ?? 30_000;
  const session = `async-${++sessionCounter}-${Date.now().toString(36)}`;

  // Create session and start command
  const newArgs = ['new-session', '-d', '-s', session];
  if (opts?.cwd) newArgs.push('-c', opts.cwd);
  tmuxRun(...newArgs);
  tmuxRun('set-option', '-t', session, 'remain-on-exit', 'on');
  tmuxRun('respawn-pane', '-k', '-t', session, '--', 'sh', '-c', cmd);

  const start = Date.now();

  return new Promise((resolve) => {
    const poll = setInterval(() => {
      try {
        const dead = tmuxRun('display-message', '-p', '-t', session, '#{pane_dead}');
        if (dead.trim() === '1' || Date.now() > start + timeoutMs) {
          clearInterval(poll);
          const exitCodeStr = tmuxRun(
            'display-message', '-p', '-t', session, '#{pane_dead_status}',
          );
          const rawOutput = tmuxRun('capture-pane', '-p', '-t', session, '-S', '-');
          try { tmuxRun('kill-session', '-t', session); } catch { /* ok */ }
          resolve({
            stdout: cleanOutput(rawOutput),
            exitCode: parseInt(exitCodeStr.trim(), 10) || 0,
          });
        }
      } catch {
        // Session may have been killed externally
        clearInterval(poll);
        resolve({ stdout: '', exitCode: 1 });
      }
    }, 200);
  });
}

/** Kill the test tmux server. Call during setup to clean stale servers. */
export function tmuxKillServer(): void {
  try {
    tmuxRun('kill-server');
  } catch {
    /* no server running — expected */
  }
}

// --- Persistent session helpers (for Steps 7-9 Claude Code tests) ---

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

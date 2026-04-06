import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { ensureTestRepo } from './integration-setup';
import { tmuxExec, shellEscape, buildEnvPrefix } from './tmux-exec';

/** File written by global-setup with the implementer worktree path. */
const IMPLEMENTER_REPO_FILE_PATH = path.join(
  path.resolve(__dirname, '../../..'),
  'node_modules',
  '.e2e-implementer-repo',
);

/** File written by global-setup with the bare remote path. */
const BARE_REMOTE_FILE_PATH = path.join(
  path.resolve(__dirname, '../../..'),
  'node_modules',
  '.e2e-bare-remote',
);

/** Source repo root — for locating the built binary. */
const SOURCE_ROOT = path.resolve(__dirname, '../../..');
export const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

/** File written by global-setup with the isolated test repo path. */
const TEST_REPO_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-test-repo');

/** File written by global-setup with the daemon's WebSocket port. */
const WS_PORT_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-ws-port');

/**
 * Get the Web UI base URL by reading the daemon port at test-execution time.
 * This avoids the stale-baseURL problem where playwright.config.ts reads the
 * port at config-parse time (before globalSetup writes it).
 */
export function getWebUIUrl(): string {
  if (existsSync(WS_PORT_FILE)) {
    const port = readFileSync(WS_PORT_FILE, 'utf-8').trim();
    if (port) return `http://localhost:${port}`;
  }
  return 'http://localhost:9999';
}

/**
 * Default test agent identity used by the global-setup daemon.
 */
export const TEST_AGENT_NAME = 'e2e_coordinator';
export const TEST_AGENT_ROLE = 'tester';
export const TEST_AGENT_MODULE = 'e2e';

/**
 * Default env object for coordinator identity.
 * Used as the env parameter when no explicit env is passed.
 */
function defaultEnv(): NodeJS.ProcessEnv {
  return {
    THRUM_NAME: TEST_AGENT_NAME,
    THRUM_ROLE: TEST_AGENT_ROLE,
    THRUM_MODULE: TEST_AGENT_MODULE,
  };
}

/**
 * Get the test repo root. Reads from the marker file written by global-setup.
 * Falls back to creating an isolated temp repo if running without global-setup
 * (e.g., individual spec files). Never falls back to the source repo.
 */
export function getTestRoot(): string {
  if (existsSync(TEST_REPO_FILE)) {
    return readFileSync(TEST_REPO_FILE, 'utf-8').trim();
  }
  return ensureTestRepo();
}

/**
 * Get the implementer worktree root.
 * Falls back to getTestRoot() if marker file doesn't exist.
 */
export function getImplementerRoot(): string {
  if (existsSync(IMPLEMENTER_REPO_FILE_PATH)) {
    return readFileSync(IMPLEMENTER_REPO_FILE_PATH, 'utf-8').trim();
  }
  return getTestRoot();
}

/**
 * Get the bare remote path for sync tests.
 */
export function getBareRemote(): string {
  if (existsSync(BARE_REMOTE_FILE_PATH)) {
    return readFileSync(BARE_REMOTE_FILE_PATH, 'utf-8').trim();
  }
  throw new Error('Bare remote marker file not found. Is global-setup configured?');
}

/**
 * Build a shell command string with env prefix and escaped args.
 */
function buildCmd(env: NodeJS.ProcessEnv | undefined, args: string[]): string {
  const prefix = buildEnvPrefix(env ?? defaultEnv());
  const escapedBin = shellEscape(BIN);
  const escapedArgs = args.map(shellEscape).join(' ');
  return prefix ? `${prefix} ${escapedBin} ${escapedArgs}` : `${escapedBin} ${escapedArgs}`;
}

/**
 * Execute a thrum CLI command inside an isolated tmux session.
 * Returns trimmed stdout. Commands run in the isolated test repo.
 *
 * tmux isolation prevents PID-based identity resolution from finding
 * the developer's Claude process in the ancestry chain.
 *
 * @param args - CLI arguments
 * @param timeout - command timeout in ms (default 10s)
 * @param env - optional environment overrides (only THRUM_* keys are used)
 */
export function thrum(args: string[], timeout = 10_000, env?: NodeJS.ProcessEnv): string {
  const cmd = buildCmd(env, args);
  const result = tmuxExec(cmd, { cwd: getTestRoot(), timeoutMs: timeout });
  if (result.exitCode !== 0) {
    throw new Error(
      `thrum ${args.join(' ')} failed (exit ${result.exitCode}):\n${result.stdout}`,
    );
  }
  return result.stdout.trim();
}

/**
 * Execute a thrum CLI command with --json flag and parse the result.
 */
export function thrumJson<T>(args: string[], env?: NodeJS.ProcessEnv): T {
  const output = thrum([...args, '--json'], 10_000, env);
  try {
    return JSON.parse(output);
  } catch {
    throw new Error(`Invalid JSON from thrum ${args.join(' ')}:\n${output}`);
  }
}

/**
 * Execute a thrum CLI command in a specific working directory.
 * Used for multi-worktree tests where coordinator and implementer
 * need to run commands from different repos.
 */
export function thrumIn(
  cwd: string,
  args: string[],
  timeout = 10_000,
  env?: NodeJS.ProcessEnv,
): string {
  const cmd = buildCmd(env, args);
  const result = tmuxExec(cmd, { cwd, timeoutMs: timeout });
  if (result.exitCode !== 0) {
    throw new Error(
      `thrum ${args.join(' ')} failed in ${cwd} (exit ${result.exitCode}):\n${result.stdout}`,
    );
  }
  return result.stdout.trim();
}

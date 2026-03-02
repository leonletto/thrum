import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { ensureTestRepo } from './integration-setup';

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
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

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
 * These env vars serve as fallback when identity file resolution fails
 * (e.g., when multiple identity files exist in .thrum/identities/).
 */
export const TEST_AGENT_ROLE = 'tester';
export const TEST_AGENT_MODULE = 'e2e';

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
 * Environment variables passed to all thrum CLI invocations.
 * THRUM_ROLE and THRUM_MODULE provide fallback identity resolution
 * so commands don't fail with "role not specified" when multiple
 * identity files exist.
 */
function thrumEnv(): NodeJS.ProcessEnv {
  return {
    ...process.env,
    THRUM_ROLE: TEST_AGENT_ROLE,
    THRUM_MODULE: TEST_AGENT_MODULE,
  };
}

/**
 * Execute a thrum CLI command safely (no shell injection).
 * Returns trimmed stdout. Commands run in the isolated test repo.
 *
 * @param args - CLI arguments
 * @param timeout - command timeout in ms (default 10s)
 * @param env - optional environment overrides
 */
export function thrum(args: string[], timeout = 10_000, env?: NodeJS.ProcessEnv): string {
  try {
    return execFileSync(BIN, args, {
      cwd: getTestRoot(),
      encoding: 'utf-8',
      timeout,
      env: env ?? thrumEnv(),
    }).trim();
  } catch (err: any) {
    const stderr = err.stderr?.toString() || '';
    throw new Error(
      `thrum ${args.join(' ')} failed (exit ${err.status}):\n${stderr}`,
    );
  }
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
  try {
    return execFileSync(BIN, args, {
      cwd,
      encoding: 'utf-8',
      timeout,
      env: env ?? thrumEnv(),
    }).trim();
  } catch (err: any) {
    const stderr = err.stderr?.toString() || '';
    throw new Error(
      `thrum ${args.join(' ')} failed in ${cwd} (exit ${err.status}):\n${stderr}`,
    );
  }
}

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';

/** Source repo root — used for building and locating the binary. */
const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

// Stable test environment path
const E2E_ROOT = '/tmp/thrum-e2e-release';
const COORDINATOR_DIR = path.join(E2E_ROOT, 'coordinator');
const IMPLEMENTER_DIR = path.join(E2E_ROOT, 'implementer');
const BARE_REMOTE_DIR = path.join(E2E_ROOT, 'bare-remote');

/**
 * Marker files written to node_modules/ so other files can find the test repos.
 */
export const TEST_REPO_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-test-repo');
export const IMPLEMENTER_REPO_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-implementer-repo');
export const BARE_REMOTE_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-bare-remote');
export const WS_PORT_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-ws-port');

function run(cmd: string, args: string[], label: string): void {
  console.log(`[global-setup] ${label}...`);
  execFileSync(cmd, args, { cwd: SOURCE_ROOT, stdio: 'inherit', timeout: 300_000 });
}

function execIn(cwd: string, cmd: string, args: string[], timeout = 30_000): string {
  return execFileSync(cmd, args, { cwd, encoding: 'utf-8', timeout, stdio: 'pipe' }).trim();
}

async function waitForDaemon(cwd: string, maxAttempts = 30, intervalMs = 1000): Promise<void> {
  console.log('[global-setup] Waiting for daemon to be ready...');
  for (let i = 0; i < maxAttempts; i++) {
    try {
      const output = execFileSync(BIN, ['daemon', 'status'], { cwd, encoding: 'utf-8', timeout: 5_000 });
      if (output.includes('running')) { console.log('[global-setup] Daemon is ready.'); return; }
    } catch { /* not ready */ }
    await new Promise(resolve => setTimeout(resolve, intervalMs));
  }
  throw new Error('Daemon did not become ready within timeout');
}

function getDaemonPort(cwd: string): number {
  const output = execFileSync(BIN, ['daemon', 'status', '--json'], { cwd, encoding: 'utf-8', timeout: 5_000 });
  const status = JSON.parse(output);
  if (!status.ws_port) throw new Error(`Daemon status missing ws_port: ${output}`);
  return status.ws_port;
}

function createCoordinatorRepo(): void {
  console.log(`[global-setup] Creating coordinator repo at ${COORDINATOR_DIR}`);
  mkdirSync(COORDINATOR_DIR, { recursive: true });

  // Git init with initial commit
  execIn(COORDINATOR_DIR, 'git', ['init']);
  execIn(COORDINATOR_DIR, 'git', ['config', 'user.email', 'e2e@test.com']);
  execIn(COORDINATOR_DIR, 'git', ['config', 'user.name', 'E2E Test']);
  writeFileSync(path.join(COORDINATOR_DIR, 'README.md'), '# E2E Test Repo\n');
  execIn(COORDINATOR_DIR, 'git', ['add', '.']);
  execIn(COORDINATOR_DIR, 'git', ['commit', '-m', 'Initial commit']);

  // thrum init (repo structure only — auto-starts daemon)
  execIn(COORDINATOR_DIR, BIN, ['init']);

  // Register coordinator agent with quickstart
  execIn(COORDINATOR_DIR, BIN, [
    'quickstart',
    '--role', 'coordinator', '--module', 'all',
    '--name', 'e2e_coordinator',
    '--intent', 'E2E test coordinator',
  ]);
}

function createImplementerWorktree(): void {
  console.log(`[global-setup] Creating implementer worktree at ${IMPLEMENTER_DIR}`);
  const setupScript = path.join(SOURCE_ROOT, 'scripts', 'setup-worktree-thrum.sh');
  execFileSync('bash', [
    setupScript,
    IMPLEMENTER_DIR, 'implementer-wt',
    '--identity', 'e2e_implementer',
    '--role', 'implementer',
    '--base', 'main',
  ], {
    cwd: COORDINATOR_DIR,
    encoding: 'utf-8',
    timeout: 30_000,
    stdio: 'inherit',
  });

  // Quickstart in implementer worktree (session + intent)
  execIn(IMPLEMENTER_DIR, BIN, [
    'quickstart',
    '--role', 'implementer', '--module', 'main',
    '--name', 'e2e_implementer',
    '--intent', 'E2E test implementer',
  ]);
}

function createBareRemote(): void {
  console.log(`[global-setup] Creating bare remote at ${BARE_REMOTE_DIR}`);
  mkdirSync(BARE_REMOTE_DIR, { recursive: true });
  execIn(BARE_REMOTE_DIR, 'git', ['init', '--bare']);

  // Add as remote to coordinator and push
  try { execIn(COORDINATOR_DIR, 'git', ['remote', 'remove', 'origin']); } catch { /* no remote */ }
  execIn(COORDINATOR_DIR, 'git', ['remote', 'add', 'origin', BARE_REMOTE_DIR]);
  execIn(COORDINATOR_DIR, 'git', ['push', 'origin', 'main']);
}

export default async function globalSetup(): Promise<void> {
  // Step 1: Build in source repo
  run('make', ['build-ui'], 'Building UI');
  run('make', ['build-go'], 'Building Go binary');
  if (!existsSync(BIN)) throw new Error(`Binary not found at ${BIN}`);

  // Step 2: Clean previous run
  if (existsSync(E2E_ROOT)) {
    console.log('[global-setup] Cleaning previous test environment...');
    // Stop daemon from previous run if still running
    try { execIn(COORDINATOR_DIR, BIN, ['daemon', 'stop']); } catch { /* not running */ }
    rmSync(E2E_ROOT, { recursive: true, force: true });
  }

  // Step 3: Create coordinator repo
  createCoordinatorRepo();

  try {
    // Step 4: Wait for daemon (auto-started by thrum init)
    await waitForDaemon(COORDINATOR_DIR);

    // Step 5: Read daemon port
    const wsPort = getDaemonPort(COORDINATOR_DIR);
    console.log(`[global-setup] Daemon WebSocket port: ${wsPort}`);

    // Step 6: Create implementer worktree
    createImplementerWorktree();

    // Step 7: Create bare remote
    createBareRemote();

    // Step 8: Write marker files
    writeFileSync(TEST_REPO_FILE, COORDINATOR_DIR);
    writeFileSync(IMPLEMENTER_REPO_FILE, IMPLEMENTER_DIR);
    writeFileSync(BARE_REMOTE_FILE, BARE_REMOTE_DIR);
    writeFileSync(WS_PORT_FILE, String(wsPort));
  } catch (err) {
    console.error('[global-setup] Setup failed, stopping daemon...');
    try { execIn(COORDINATOR_DIR, BIN, ['daemon', 'stop']); } catch { /* best effort */ }
    throw err;
  }

  console.log('[global-setup] Setup complete.');
}

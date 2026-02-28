import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, writeFileSync } from 'node:fs';
import * as os from 'node:os';
import path from 'node:path';

/** Source repo root — used for building and locating the binary. */
const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

/**
 * Marker files written to node_modules/ so other files can find the test repo.
 */
export const TEST_REPO_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-test-repo');
export const WS_PORT_FILE = path.join(SOURCE_ROOT, 'node_modules', '.e2e-ws-port');
export const DAEMON_OWNER_MARKER = path.join(SOURCE_ROOT, 'node_modules', '.e2e-daemon-owner');

const TEST_AGENT_ROLE = 'tester';
const TEST_AGENT_MODULE = 'e2e';

function daemonEnv(): NodeJS.ProcessEnv {
  return {
    ...process.env,
    THRUM_ROLE: TEST_AGENT_ROLE,
    THRUM_MODULE: TEST_AGENT_MODULE,
  };
}

function run(cmd: string, args: string[], label: string): void {
  console.log(`[global-setup] ${label}...`);
  execFileSync(cmd, args, {
    cwd: SOURCE_ROOT,
    stdio: 'inherit',
    timeout: 300_000,
  });
}

async function waitForDaemon(cwd: string, maxAttempts = 30, intervalMs = 1000): Promise<void> {
  console.log('[global-setup] Waiting for daemon to be ready...');
  for (let i = 0; i < maxAttempts; i++) {
    try {
      const output = execFileSync(BIN, ['daemon', 'status'], {
        cwd,
        encoding: 'utf-8',
        timeout: 5_000,
      });
      if (output.includes('running')) {
        console.log('[global-setup] Daemon is ready.');
        return;
      }
    } catch {
      // daemon not yet ready
    }
    await new Promise(resolve => setTimeout(resolve, intervalMs));
  }
  throw new Error('Daemon did not become ready within timeout');
}

function getDaemonPort(cwd: string): number {
  const output = execFileSync(BIN, ['daemon', 'status', '--json'], {
    cwd,
    encoding: 'utf-8',
    timeout: 5_000,
  });
  const status = JSON.parse(output);
  if (!status.ws_port) {
    throw new Error(`Daemon status missing ws_port: ${output}`);
  }
  return status.ws_port;
}

/**
 * Create an isolated temp git repo for E2E tests.
 * `thrum init` automatically starts a daemon and registers a default agent.
 * Returns the absolute path to the temp directory.
 */
function createTestRepo(): string {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'thrum-e2e-'));
  console.log(`[global-setup] Created test repo at ${dir}`);

  // Initialize git repo with a commit (required for thrum init)
  execFileSync('git', ['init'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', 'e2e@test.com'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', 'E2E Test'], { cwd: dir, stdio: 'pipe' });

  // Create a dummy file and initial commit
  writeFileSync(path.join(dir, 'README.md'), '# E2E Test Repo\n');
  execFileSync('git', ['add', '.'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['commit', '-m', 'Initial commit'], { cwd: dir, stdio: 'pipe' });

  // Initialize thrum — this also starts a daemon and registers the test agent
  execFileSync(BIN, [
    'init',
    '--agent-role', 'tester',
    '--agent-module', 'e2e',
    '--agent-name', 'e2e_tester',
  ], {
    cwd: dir,
    encoding: 'utf-8',
    timeout: 30_000,
    env: daemonEnv(),
  });

  return dir;
}

export default async function globalSetup(): Promise<void> {
  // Step 1: Build UI and Go binary in source repo
  run('make', ['build-ui'], 'Building UI');
  run('make', ['build-go'], 'Building Go binary');

  if (!existsSync(BIN)) {
    throw new Error(`Binary not found at ${BIN}`);
  }

  // Step 2: Create isolated test repo (thrum init starts daemon automatically)
  const testRepo = createTestRepo();
  writeFileSync(TEST_REPO_FILE, testRepo);
  writeFileSync(DAEMON_OWNER_MARKER, 'owned');

  try {
    // Step 3: Wait for daemon to be ready
    await waitForDaemon(testRepo);

    // Step 4: Read the daemon's WebSocket port and write for playwright.config.ts
    const wsPort = getDaemonPort(testRepo);
    writeFileSync(WS_PORT_FILE, String(wsPort));
    console.log(`[global-setup] Daemon WebSocket port: ${wsPort}`);

    // Note: thrum init already registered an agent (e2e_tester with role=tester,
    // module=e2e) and started a session. No additional quickstart needed.
  } catch (err) {
    console.error('[global-setup] Setup failed after daemon start, stopping daemon...');
    try {
      execFileSync(BIN, ['daemon', 'stop'], { cwd: testRepo, timeout: 5_000 });
    } catch { /* best effort cleanup */ }
    throw err;
  }

  console.log('[global-setup] Setup complete.');
}

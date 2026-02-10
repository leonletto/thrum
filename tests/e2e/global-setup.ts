import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import path from 'node:path';

const ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(ROOT, 'bin', 'thrum');

/**
 * Default test agent identity. Must match the constants in helpers/thrum-cli.ts.
 * The daemon is started with THRUM_ROLE and THRUM_MODULE as env vars so that
 * identity resolution always has a fallback when multiple identity files exist
 * in .thrum/identities/.
 */
const TEST_AGENT_ROLE = 'tester';
const TEST_AGENT_MODULE = 'e2e';

/**
 * Environment for daemon and CLI processes.
 * THRUM_ROLE / THRUM_MODULE provide fallback identity resolution.
 */
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
    cwd: ROOT,
    stdio: 'inherit',
    timeout: 300_000, // 5 min for builds
  });
}

async function waitForDaemon(maxAttempts = 30, intervalMs = 1000): Promise<void> {
  console.log('[global-setup] Waiting for daemon to be ready...');
  for (let i = 0; i < maxAttempts; i++) {
    try {
      const output = execFileSync(BIN, ['daemon', 'status'], {
        cwd: ROOT,
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

export default async function globalSetup(): Promise<void> {
  // Step 1: Build UI (copies dist to internal/web/dist/)
  run('make', ['build-ui'], 'Building UI');

  // Step 2: Build Go binary with embedded UI
  run('make', ['build-go'], 'Building Go binary');

  if (!existsSync(BIN)) {
    throw new Error(`Binary not found at ${BIN}`);
  }

  // Step 3: Start daemon with identity env vars for fallback resolution
  console.log('[global-setup] Starting daemon...');
  execFileSync(BIN, ['daemon', 'start'], {
    cwd: ROOT,
    stdio: 'inherit',
    timeout: 15_000,
    env: daemonEnv(),
  });

  // If anything after daemon start fails, stop the daemon so port 9999
  // doesn't stay bound and block the next test run.
  try {
    // Step 4: Wait for daemon to be ready
    await waitForDaemon();

    // Step 5: Register a test agent for CLI operations
    console.log('[global-setup] Registering test agent...');
    try {
      execFileSync(BIN, [
        'quickstart',
        '--role', TEST_AGENT_ROLE,
        '--module', TEST_AGENT_MODULE,
        '--display', 'E2E Test Agent',
        '--intent', 'Running Playwright E2E tests',
      ], {
        cwd: ROOT,
        encoding: 'utf-8',
        timeout: 10_000,
        env: daemonEnv(),
      });
    } catch (err: any) {
      const stderr = err.stderr?.toString() || err.message || '';
      if (stderr.toLowerCase().includes('already') || stderr.toLowerCase().includes('exists')) {
        console.log('[global-setup] Agent already registered, continuing.');
      } else {
        throw new Error(`Agent registration failed unexpectedly: ${stderr}`);
      }
    }
  } catch (err) {
    console.error('[global-setup] Setup failed after daemon start, stopping daemon...');
    try {
      execFileSync(BIN, ['daemon', 'stop'], { cwd: ROOT, timeout: 5_000 });
    } catch { /* best effort cleanup */ }
    throw err;
  }

  console.log('[global-setup] Setup complete.');
}

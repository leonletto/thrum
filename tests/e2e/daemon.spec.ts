/**
 * Daemon Management Tests — SC-42 to SC-48
 *
 * These tests exercise the daemon lifecycle: start, stop, double-start
 * prevention, restart, health check, WebSocket endpoint, and embedded UI.
 *
 * Each test uses its own isolated temp repo so it doesn't conflict with
 * the global-setup daemon that other spec files rely on.
 */
import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import * as http from 'node:http';

const ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(ROOT, 'bin', 'thrum');
const WS_PORT_FILE = path.join(ROOT, 'node_modules', '.e2e-ws-port');

/** Read the global daemon port written by global-setup. */
function getGlobalDaemonPort(): number {
  if (fs.existsSync(WS_PORT_FILE)) {
    const content = fs.readFileSync(WS_PORT_FILE, 'utf-8').trim();
    const port = parseInt(content, 10);
    if (!isNaN(port)) return port;
  }
  return 9999; // fallback
}

/** Create an isolated temp repo with thrum initialized but daemon stopped. */
function createTestRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-daemon-'));
  execFileSync('git', ['init'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', 'test@test.com'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', 'Test User'], { cwd: dir, stdio: 'pipe' });
  fs.writeFileSync(path.join(dir, 'README.md'), '# Daemon Test Repo\n');
  execFileSync('git', ['add', '.'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['commit', '-m', 'Initial commit'], { cwd: dir, stdio: 'pipe' });
  // thrum init auto-starts a daemon; stop it so daemon tests control lifecycle
  execFileSync(BIN, ['init'], { cwd: dir, encoding: 'utf-8', timeout: 30_000 });
  try {
    execFileSync(BIN, ['daemon', 'stop'], { cwd: dir, encoding: 'utf-8', timeout: 10_000 });
  } catch { /* may not be running */ }
  return dir;
}

/** Run thrum in a specific repo. */
function thrumIn(repo: string, args: string[]): string {
  return execFileSync(BIN, ['--repo', repo, ...args], {
    cwd: repo,
    encoding: 'utf-8',
    timeout: 15_000,
  }).trim();
}

/** Stop daemon in repo, swallowing errors (cleanup helper). */
function stopDaemonSafe(repo: string): void {
  try {
    execFileSync(BIN, ['--repo', repo, 'daemon', 'stop'], {
      cwd: repo,
      encoding: 'utf-8',
      timeout: 10_000,
    });
  } catch {
    // already stopped
  }
}

/** Wait for daemon to become ready. */
async function waitForDaemonReady(repo: string, maxAttempts = 20): Promise<void> {
  for (let i = 0; i < maxAttempts; i++) {
    try {
      const out = thrumIn(repo, ['daemon', 'status']);
      if (out.includes('running')) return;
    } catch {
      // not ready yet
    }
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  throw new Error('Daemon did not become ready');
}

/** Wait for process to exit. */
async function waitForProcessExit(pid: number, maxAttempts = 20): Promise<void> {
  for (let i = 0; i < maxAttempts; i++) {
    try {
      process.kill(pid, 0);
      // Process still exists, wait longer
      await new Promise(resolve => setTimeout(resolve, 100));
    } catch (e: any) {
      if (e.code === 'ESRCH') {
        // Process doesn't exist, success
        return;
      }
      throw e;
    }
  }
  throw new Error(`Process ${pid} did not exit`);
}

/** Clean up temp repo dir. */
function cleanupRepo(dir: string): void {
  fs.rmSync(dir, { recursive: true, force: true });
}

test.describe('Daemon Management', () => {
  test.describe.configure({ mode: 'serial' });

  test('SC-42: Start daemon', async () => {
    const repo = createTestRepo();
    try {
      const output = thrumIn(repo, ['daemon', 'start']);
      expect(output.toLowerCase()).toMatch(/start|running/);

      await waitForDaemonReady(repo);

      const status = thrumIn(repo, ['daemon', 'status']);
      expect(status.toLowerCase()).toContain('running');
    } finally {
      stopDaemonSafe(repo);
      cleanupRepo(repo);
    }
  });

  test('SC-43: Stop daemon cleanly, no zombies', async () => {
    const repo = createTestRepo();
    try {
      thrumIn(repo, ['daemon', 'start']);
      await waitForDaemonReady(repo);

      // Get PID from status
      const statusJson = thrumIn(repo, ['daemon', 'status', '--json']);
      const parsed = JSON.parse(statusJson);
      const pid = parsed.pid;
      expect(typeof pid).toBe('number');

      // Stop the daemon
      thrumIn(repo, ['daemon', 'stop']);

      // Poll for process to exit
      await waitForProcessExit(pid);

      // Verify process no longer exists
      let processExists = false;
      try {
        process.kill(pid, 0);
        processExists = true;
      } catch (e: any) {
        expect(e.code).toBe('ESRCH');
      }
      expect(processExists).toBe(false);

      // Verify status shows not running
      const afterStatus = thrumIn(repo, ['daemon', 'status']);
      expect(afterStatus.toLowerCase()).toMatch(/not running|stopped/);
    } finally {
      stopDaemonSafe(repo);
      cleanupRepo(repo);
    }
  });

  test('SC-44: Double-start prevention', async () => {
    const repo = createTestRepo();
    try {
      thrumIn(repo, ['daemon', 'start']);
      await waitForDaemonReady(repo);

      // Second start should fail or indicate already running
      let secondStartOutput: string;
      try {
        secondStartOutput = thrumIn(repo, ['daemon', 'start']);
      } catch (e: any) {
        secondStartOutput = e.stderr?.toString() || e.stdout?.toString() || e.message;
      }
      expect(secondStartOutput.toLowerCase()).toMatch(/already running|already started/);
    } finally {
      stopDaemonSafe(repo);
      cleanupRepo(repo);
    }
  });

  test('SC-45: Daemon restart', async () => {
    const repo = createTestRepo();
    try {
      thrumIn(repo, ['daemon', 'start']);
      await waitForDaemonReady(repo);

      // Get original PID
      const origJson = thrumIn(repo, ['daemon', 'status', '--json']);
      const origPid = JSON.parse(origJson).pid;

      // Restart (stop then start, or use restart command)
      try {
        thrumIn(repo, ['daemon', 'restart']);
      } catch {
        // If restart command fails, do manual stop+start
        thrumIn(repo, ['daemon', 'stop']);
        // Poll for old daemon to exit before starting new one
        await waitForProcessExit(origPid);
        thrumIn(repo, ['daemon', 'start']);
      }

      await waitForDaemonReady(repo);

      // Verify new PID
      const newJson = thrumIn(repo, ['daemon', 'status', '--json']);
      const newPid = JSON.parse(newJson).pid;
      expect(newPid).not.toBe(origPid);
    } finally {
      stopDaemonSafe(repo);
      cleanupRepo(repo);
    }
  });

  test('SC-46: Health check via RPC', async () => {
    const repo = createTestRepo();
    try {
      thrumIn(repo, ['daemon', 'start']);
      await waitForDaemonReady(repo);

      const statusJson = thrumIn(repo, ['daemon', 'status', '--json']);
      const parsed = JSON.parse(statusJson);

      // Verify health response fields
      expect(typeof parsed.pid).toBe('number');
      expect(parsed.status ?? parsed.state).toMatch(/running|healthy|ok/i);
    } finally {
      stopDaemonSafe(repo);
      cleanupRepo(repo);
    }
  });

  test('SC-47: Daemon serves WebSocket', async () => {
    // This test uses the globally running daemon (from global-setup)
    // since we need a known port to connect to.
    const port = getGlobalDaemonPort();
    const result = await new Promise<{ statusCode: number; headers: http.IncomingHttpHeaders }>((resolve, reject) => {
      const req = http.get(
        `http://localhost:${port}/ws`,
        {
          headers: {
            'Upgrade': 'websocket',
            'Connection': 'Upgrade',
            'Sec-WebSocket-Key': Buffer.from('0123456789abcdef').toString('base64'),
            'Sec-WebSocket-Version': '13',
          },
        },
      );
      req.on('upgrade', (res, socket) => {
        resolve({ statusCode: 101, headers: res.headers });
        socket.destroy();
      });
      req.on('response', (res) => {
        // If server rejects the upgrade, we get a normal response
        resolve({ statusCode: res.statusCode!, headers: res.headers });
        res.destroy();
      });
      req.on('error', reject);
      req.setTimeout(5000, () => {
        req.destroy();
        reject(new Error('Request timed out'));
      });
    });

    // WebSocket upgrade returns 101 Switching Protocols
    expect(result.statusCode).toBe(101);
    expect(result.headers['upgrade']?.toLowerCase()).toBe('websocket');
  });

  test('SC-48: Daemon serves embedded UI', async ({ page }) => {
    // Use the globally running daemon
    const response = await page.goto('/');
    expect(response?.status()).toBe(200);

    // Should serve the React SPA
    const html = await page.content();
    expect(html.toLowerCase()).toContain('<!doctype html');

    // Should have loaded CSS and JS
    await expect(page.locator('body')).not.toBeEmpty();
  });
});

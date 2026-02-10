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

/** Create an isolated temp repo with thrum initialized. */
function createTestRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-daemon-'));
  execFileSync('git', ['init'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', 'test@test.com'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', 'Test User'], { cwd: dir, stdio: 'pipe' });
  execFileSync(BIN, ['init'], { cwd: dir, encoding: 'utf-8', timeout: 10_000 });
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

      // Give OS time to clean up the process
      await new Promise(resolve => setTimeout(resolve, 1000));

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
        await new Promise(resolve => setTimeout(resolve, 1000));
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
    // Note: Node.js http.get fires 'upgrade' event (not the response callback)
    // when the server responds with 101 Switching Protocols.
    const result = await new Promise<{ statusCode: number; headers: http.IncomingHttpHeaders }>((resolve, reject) => {
      const req = http.get(
        'http://localhost:9999/ws',
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

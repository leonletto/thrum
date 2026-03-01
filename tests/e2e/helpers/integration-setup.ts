/**
 * Integration Test Setup — On-Demand Isolated Repo
 *
 * Creates a minimal thrum test repo in a random temp directory when
 * tests run without the full E2E global-setup (e.g., running individual
 * spec files). This prevents tests from accidentally modifying the
 * live .thrum directory in the source repo.
 *
 * Properties:
 * - Random temp dir: no collisions with E2E suite or other agents
 * - Idempotent: multiple calls return the same repo within a process
 * - Auto-cleanup: daemon stopped + temp dir removed on process exit
 */

import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const SOURCE_ROOT = path.resolve(__dirname, '../../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

let cachedTestRoot: string | null = null;
let cleanupRegistered = false;

function execIn(cwd: string, cmd: string, args: string[], timeout = 30_000): string {
  return execFileSync(cmd, args, {
    cwd,
    encoding: 'utf-8',
    timeout,
    stdio: 'pipe',
  }).trim();
}

function waitForDaemon(cwd: string, maxAttempts = 30, intervalMs = 1000): void {
  for (let i = 0; i < maxAttempts; i++) {
    try {
      const out = execIn(cwd, BIN, ['daemon', 'status']);
      if (out.includes('running')) return;
    } catch {
      // daemon not ready yet
    }
    execFileSync('sleep', [String(intervalMs / 1000)]);
  }
  throw new Error(`Daemon failed to start in ${cwd} after ${maxAttempts}s`);
}

/**
 * Create and return the path to an isolated test repo.
 * Idempotent within a single process — returns the same path on repeat calls.
 * The repo and its daemon are cleaned up when the process exits.
 */
export function ensureTestRepo(): string {
  if (cachedTestRoot) return cachedTestRoot;

  const dir = mkdtempSync(path.join(os.tmpdir(), 'thrum-test-'));

  // Initialize a git repo (thrum requires one)
  execIn(dir, 'git', ['init']);
  execIn(dir, 'git', ['config', 'user.email', 'test@test.com']);
  execIn(dir, 'git', ['config', 'user.name', 'Test']);
  writeFileSync(path.join(dir, 'README.md'), '# Test Repo\n');
  execIn(dir, 'git', ['add', '.']);
  execIn(dir, 'git', ['commit', '-m', 'Initial commit']);

  // Initialize thrum (starts daemon)
  execIn(dir, BIN, ['init']);
  waitForDaemon(dir);

  // Register a test agent
  execIn(dir, BIN, [
    'quickstart',
    '--role', 'tester',
    '--module', 'test',
    '--name', 'test_agent',
    '--intent', 'Integration test agent',
  ]);

  // Register cleanup on process exit
  if (!cleanupRegistered) {
    cleanupRegistered = true;
    process.on('exit', () => {
      if (!cachedTestRoot) return;
      try {
        execFileSync(BIN, ['daemon', 'stop'], {
          cwd: cachedTestRoot,
          timeout: 5_000,
          stdio: 'ignore',
        });
      } catch {
        // best-effort
      }
      try {
        rmSync(cachedTestRoot, { recursive: true, force: true });
      } catch {
        // best-effort
      }
    });
  }

  cachedTestRoot = dir;
  return dir;
}

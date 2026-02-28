import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, rmSync, unlinkSync } from 'node:fs';
import path from 'node:path';
import { TEST_REPO_FILE, WS_PORT_FILE, DAEMON_OWNER_MARKER } from './global-setup.js';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

export default async function globalTeardown(): Promise<void> {
  // Read test repo path
  let testRepo: string | undefined;
  try {
    if (existsSync(TEST_REPO_FILE)) {
      testRepo = readFileSync(TEST_REPO_FILE, 'utf-8').trim();
    }
  } catch { /* best effort */ }

  // Check if we own the daemon
  let owned = true;
  try {
    if (existsSync(DAEMON_OWNER_MARKER)) {
      const content = readFileSync(DAEMON_OWNER_MARKER, 'utf-8').trim();
      owned = content === 'owned';
    }
  } catch { /* best effort */ }

  // Stop daemon if we started it
  if (owned && testRepo) {
    console.log('[global-teardown] Stopping daemon in test repo...');
    try {
      execFileSync(BIN, ['daemon', 'stop'], {
        cwd: testRepo,
        encoding: 'utf-8',
        timeout: 15_000,
      });
      console.log('[global-teardown] Daemon stopped.');
    } catch (err: any) {
      const stderr = err.stderr?.toString() || err.message || '';
      if (stderr.toLowerCase().includes('not running') || stderr.toLowerCase().includes('no daemon')) {
        console.log('[global-teardown] Daemon was not running (already stopped).');
      } else {
        console.error('[global-teardown] Unexpected error stopping daemon:', stderr);
      }
    }
  } else if (!owned) {
    console.log('[global-teardown] Daemon was already running before tests — leaving it running.');
  }

  // Clean up temp test repo
  if (testRepo && existsSync(testRepo)) {
    console.log(`[global-teardown] Removing test repo: ${testRepo}`);
    try {
      rmSync(testRepo, { recursive: true, force: true });
    } catch (err) {
      console.error('[global-teardown] Failed to clean up test repo:', err);
    }
  }

  // Remove marker files
  for (const file of [TEST_REPO_FILE, WS_PORT_FILE, DAEMON_OWNER_MARKER]) {
    try {
      if (existsSync(file)) unlinkSync(file);
    } catch { /* best effort */ }
  }
}

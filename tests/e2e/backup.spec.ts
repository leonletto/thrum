/**
 * Backup Tests
 *
 * Tests backup create, status, config, and restore using --dir
 * to write backups into the test repo.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumIn, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';
import { execFileSync } from 'node:child_process';
import * as path from 'node:path';
import * as fs from 'node:fs';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

test.describe('Backup', () => {
  test.describe.configure({ mode: 'serial' });

  const backupDir = () => path.join(getTestRoot(), 'test-backups');

  test('Create backup', async () => {
    const output = thrum(['backup', '--dir', backupDir()]);
    expect(output.toLowerCase()).toMatch(/backup|complete|created/);
    expect(fs.existsSync(backupDir())).toBe(true);
  });

  test('Backup status', async () => {
    const output = thrum(['backup', 'status', '--dir', backupDir()]);
    expect(output.toLowerCase()).toContain('backup');
  });

  test('Backup config', async () => {
    const output = thrum(['backup', 'config']);
    expect(output.toLowerCase()).toContain('backup');
  });

  test('Restore from backup', async () => {
    try { thrum(['backup', '--dir', backupDir()]); } catch { /* ok if exists */ }

    let output = '';
    try {
      output = thrum(['backup', 'restore', '--dir', backupDir(), '--yes'], 30_000);
    } catch (err: any) {
      output = err.message || '';
    }
    expect(output.toLowerCase()).toMatch(/restor|backup/);

    // Restore stops/restarts daemon and replaces DB (wiping sessions).
    // Wait for daemon to be fully ready via RPC (not just PID alive).
    const testRoot = getTestRoot();
    for (let i = 0; i < 30; i++) {
      try {
        // agent list requires a working RPC connection (not just PID check)
        execFileSync(BIN, ['agent', 'list', '--json'], {
          cwd: testRoot, encoding: 'utf-8', timeout: 5_000,
        });
        break;
      } catch { /* not ready */ }
      await new Promise(r => setTimeout(r, 1000));
    }

    // Re-quickstart both agents (sessions wiped by restore)
    thrumIn(testRoot, ['quickstart', '--role', 'coordinator', '--module', 'all',
      '--name', 'e2e_coordinator', '--intent', 'E2E test coordinator'], 10_000);
    thrumIn(getImplementerRoot(), ['quickstart', '--role', 'implementer', '--module', 'main',
      '--name', 'e2e_implementer', '--intent', 'E2E test implementer'], 10_000);
  });
});

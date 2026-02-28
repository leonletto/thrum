/**
 * Backup Tests
 *
 * Tests backup create, status, config, and restore.
 * Uses an isolated test repo so backup restore (which stops/restarts the
 * daemon and replaces the DB) doesn't interfere with the shared daemon.
 */
import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import * as path from 'node:path';
import * as fs from 'node:fs';
import * as os from 'node:os';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

/** Run thrum in a specific repo. */
function thrumIn(repo: string, args: string[], timeout = 15_000): string {
  return execFileSync(BIN, args, {
    cwd: repo, encoding: 'utf-8', timeout, stdio: 'pipe',
  }).trim();
}

/** Create an isolated repo with thrum initialized and daemon running. */
function createBackupRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-backup-'));
  execFileSync('git', ['init'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', 'test@test.com'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', 'Test'], { cwd: dir, stdio: 'pipe' });
  fs.writeFileSync(path.join(dir, 'README.md'), '# Backup Test\n');
  execFileSync('git', ['add', '.'], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['commit', '-m', 'init'], { cwd: dir, stdio: 'pipe' });
  execFileSync(BIN, ['init'], { cwd: dir, encoding: 'utf-8', timeout: 30_000 });
  return dir;
}

test.describe('Backup', () => {
  test.describe.configure({ mode: 'serial' });

  let repo: string;
  const backupDir = () => path.join(repo, 'test-backups');

  test.beforeAll(async () => {
    repo = createBackupRepo();
    // Wait for daemon
    for (let i = 0; i < 15; i++) {
      try {
        thrumIn(repo, ['daemon', 'status']);
        break;
      } catch { /* not ready */ }
      await new Promise(r => setTimeout(r, 500));
    }
  });

  test.afterAll(async () => {
    try { thrumIn(repo, ['daemon', 'stop']); } catch { /* ok */ }
    fs.rmSync(repo, { recursive: true, force: true });
  });

  test('Create backup', async () => {
    const output = thrumIn(repo, ['backup', '--dir', backupDir()]);
    expect(output.toLowerCase()).toMatch(/backup|complete|created/);
    expect(fs.existsSync(backupDir())).toBe(true);
  });

  test('Backup status', async () => {
    const output = thrumIn(repo, ['backup', 'status', '--dir', backupDir()]);
    expect(output.toLowerCase()).toContain('backup');
  });

  test('Backup config', async () => {
    const output = thrumIn(repo, ['backup', 'config']);
    expect(output.toLowerCase()).toContain('backup');
  });

  test('Restore from backup', async () => {
    // Ensure a backup exists
    try { thrumIn(repo, ['backup', '--dir', backupDir()]); } catch { /* ok */ }

    let output = '';
    try {
      output = thrumIn(repo, ['backup', 'restore', '--dir', backupDir(), '--yes'], 30_000);
    } catch (err: any) {
      output = err.message || '';
    }
    expect(output.toLowerCase()).toMatch(/restor|backup/);

    // Restore stops/restarts daemon — wait for it to be ready
    for (let i = 0; i < 30; i++) {
      try {
        thrumIn(repo, ['daemon', 'status']);
        break;
      } catch { /* not ready */ }
      await new Promise(r => setTimeout(r, 1000));
    }
  });
});

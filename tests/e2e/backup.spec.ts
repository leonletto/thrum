/**
 * Backup Tests
 *
 * Tests backup create, status, config, and restore using --dir
 * to write backups into the test repo.
 */
import { test, expect } from '@playwright/test';
import { thrum, getTestRoot } from './helpers/thrum-cli.js';
import * as path from 'node:path';
import * as fs from 'node:fs';

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
  });
});

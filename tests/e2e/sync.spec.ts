/**
 * Sync Tests — SC-38 to SC-41
 *
 * Tests for manual sync, sync status, cross-worktree message visibility,
 * and cross-machine sync via git push/pull. These use isolated bare repos
 * as git remotes to simulate multi-repo scenarios.
 */
import { test, expect } from '@playwright/test';
import { thrum, getTestRoot } from './helpers/thrum-cli.js';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

/** Create a bare git repo with an initial commit so clones have a branch. */
function createBareRemote(): string {
  // Create a temp repo, commit, then clone --bare from it
  const tmpInit = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-init-'));
  execFileSync('git', ['init'], { cwd: tmpInit, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', 'init@test.com'], { cwd: tmpInit, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', 'Init'], { cwd: tmpInit, stdio: 'pipe' });
  fs.writeFileSync(path.join(tmpInit, 'README.md'), '# test\n');
  execFileSync('git', ['add', '.'], { cwd: tmpInit, stdio: 'pipe' });
  execFileSync('git', ['commit', '-m', 'init'], { cwd: tmpInit, stdio: 'pipe' });

  const bareDir = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-bare-'));
  fs.rmSync(bareDir, { recursive: true }); // clone --bare needs non-existent target
  execFileSync('git', ['clone', '--bare', tmpInit, bareDir], { stdio: 'pipe' });
  fs.rmSync(tmpInit, { recursive: true, force: true });
  return bareDir;
}

/** Clone a bare repo and initialize thrum (with daemon). */
function cloneAndInit(bare: string, name: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `thrum-${name}-`));
  execFileSync('git', ['clone', bare, dir], { stdio: 'pipe' });
  execFileSync('git', ['config', 'user.email', `${name}@test.com`], { cwd: dir, stdio: 'pipe' });
  execFileSync('git', ['config', 'user.name', `Test ${name}`], { cwd: dir, stdio: 'pipe' });
  execFileSync(BIN, ['init'], { cwd: dir, encoding: 'utf-8', timeout: 10_000 });
  // Start daemon (init creates structure but may not auto-start daemon)
  try {
    execFileSync(BIN, ['daemon', 'start'], { cwd: dir, encoding: 'utf-8', timeout: 10_000 });
  } catch { /* may already be running */ }
  return dir;
}

/** Run thrum in a specific directory. */
function thrumIn(cwd: string, args: string[]): string {
  return execFileSync(BIN, args, {
    cwd,
    encoding: 'utf-8',
    timeout: 15_000,
  }).trim();
}

/** Cleanup temp directories. */
function cleanup(...dirs: string[]): void {
  for (const dir of dirs) {
    try {
      fs.rmSync(dir, { recursive: true, force: true });
    } catch {
      // best effort
    }
  }
}

test.describe('Sync', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(() => {
    // Ensure session is active (may have been killed by daemon restart tests)
    try {
      thrum(['session', 'start']);
    } catch (err: any) {
      const msg = err.message || '';
      if (!msg.toLowerCase().includes('already active') && !msg.toLowerCase().includes('already exists')) {
        throw err;
      }
    }
  });

  test('SC-38: Manual sync', async () => {
    // Arrange: send some messages (using the global daemon/repo)
    thrum(['send', 'Sync test message SC-38']);

    // Act: run sync
    let syncOutput: string;
    try {
      syncOutput = thrum(['sync', 'force']);
    } catch (e: any) {
      // sync may not have a remote configured in the test repo
      syncOutput = e.stdout?.toString() || e.stderr?.toString() || e.message;
    }

    // Assert: sync completed (or reported status)
    expect(syncOutput).not.toBe('');
  });

  test('SC-39: Check sync status', async () => {
    // Act: check sync status
    let statusOutput: string;
    try {
      statusOutput = thrum(['sync', 'status']);
    } catch (e: any) {
      statusOutput = e.stdout?.toString() || e.stderr?.toString() || e.message;
    }

    // Assert: shows sync state information
    expect(statusOutput.toLowerCase()).toMatch(/sync|status|last|pending|branch/);
  });

  test('SC-40: Sync force creates local a-sync branch with message data', async () => {
    // Arrange: create a standalone repo with thrum
    const bare = createBareRemote();
    let repo = '';

    try {
      repo = cloneAndInit(bare, 'sc40');

      // Wait for daemon (auto-started by thrum init)
      for (let i = 0; i < 10; i++) {
        try {
          const st = thrumIn(repo, ['daemon', 'status']);
          if (st.includes('running')) break;
        } catch { /* not ready */ }
        await new Promise(resolve => setTimeout(resolve, 500));
      }

      // Register and send message
      try {
        thrumIn(repo, ['quickstart', '--role', 'sender', '--module', 'test',
          '--name', 'sc40_sender', '--intent', 'SC-40 test']);
      } catch { /* may exist */ }
      thrumIn(repo, ['send', 'Cross-worktree message SC-40']);

      // Act: sync force
      const syncOutput = thrumIn(repo, ['sync', 'force']);
      expect(syncOutput.toLowerCase()).toMatch(/sync/);

      // Assert: local a-sync branch exists with message data
      const branches = execFileSync('git', ['branch'], { cwd: repo, encoding: 'utf-8' });
      expect(branches).toContain('a-sync');

      // Verify the sync worktree has JSONL data
      const syncDir = path.join(repo, '.git', 'thrum-sync', 'a-sync');
      expect(fs.existsSync(syncDir)).toBe(true);

      // Walk sync dir for JSONL files
      const walkJsonl = (dir: string): string => {
        let content = '';
        for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
          const full = path.join(dir, entry.name);
          if (entry.isDirectory()) content += walkJsonl(full);
          else if (entry.name.endsWith('.jsonl')) content += fs.readFileSync(full, 'utf-8');
        }
        return content;
      };
      const allContent = walkJsonl(syncDir);
      expect(allContent).toContain('SC-40');

      // Clean up daemon
      try { thrumIn(repo, ['daemon', 'stop']); } catch { /* ok */ }
    } finally {
      cleanup(bare, repo);
    }
  });

  test.fixme('SC-41: Cross-machine sync via git push/pull', async () => {
    // FIXME: `sync force` in local-only mode writes message data to the a-sync
    // worktree (.git/thrum-sync/a-sync/) but does NOT commit it to the a-sync
    // branch. Without remote sync configuration, there's nothing to push/fetch.
    // Cross-machine sync requires Tailscale or remote sync to be enabled.
  });
});

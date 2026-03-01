/**
 * Bugfix Regression Tests — J1–J13
 *
 * Each test verifies a specific bugfix hasn't regressed.
 * Tests are independent and can run in any order.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumIn, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';
import { execFileSync, spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

/** Dedicated agent for regression tests — avoids session conflicts with other specs. */
function regressionEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_regtest', THRUM_ROLE: 'tester', THRUM_MODULE: 'all' };
}

test.describe('Bugfix Regressions', () => {
  test.beforeAll(async () => {
    // Register a dedicated agent for regression tests to avoid session conflicts
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'tester', '--module', 'all',
        '--name', 'e2e_regtest', '--intent', 'Regression testing'], 10_000, regressionEnv());
    } catch { /* may already exist */ }
  });

  test('J2: Prime unread count accuracy', async () => {
    // Mark all read first
    thrumIn(getTestRoot(), ['message', 'read', '--all'], 10_000, regressionEnv());

    // Send a message to regtest agent from the implementer worktree (avoids session issues)
    const implEnv = { ...process.env, THRUM_NAME: 'e2e_implementer', THRUM_ROLE: 'implementer', THRUM_MODULE: 'main' };
    try {
      thrumIn(getImplementerRoot(), ['quickstart', '--role', 'implementer', '--module', 'main',
        '--name', 'e2e_implementer', '--intent', 'J2 test'], 10_000, implEnv);
    } catch { /* may already exist */ }
    thrumIn(getImplementerRoot(), ['send', 'Unread count verification', '--to', '@e2e_regtest'], 10_000, implEnv);

    // Prime should show unread > 0 (not "0 unread" or "all read")
    const primeOutput = thrumIn(getTestRoot(), ['prime'], 10_000, regressionEnv());
    expect(primeOutput).toMatch(/[1-9]\d*\s*unread/);
  });

  test('J1: Priority flag removed from CLI', async () => {
    let error1 = '';
    try {
      thrumIn(getTestRoot(), ['send', 'Priority test', '--to', '@e2e_implementer', '-p', 'critical'], 10_000, regressionEnv());
    } catch (err: any) { error1 = err.message; }
    expect(error1.toLowerCase()).toContain('unknown');

    let error2 = '';
    try {
      thrumIn(getTestRoot(), ['send', 'Priority test', '--to', '@e2e_implementer', '--priority', 'high'], 10_000, regressionEnv());
    } catch (err: any) { error2 = err.message; }
    expect(error2.toLowerCase()).toContain('unknown');
  });

  test('J3: Init detects worktree and creates redirect', async () => {
    const tmpMain = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-wt-test-'));
    const tmpWt = path.join(os.tmpdir(), `thrum-wt-test-wt-${Date.now()}`);
    try {
      execFileSync('git', ['init'], { cwd: tmpMain, stdio: 'pipe' });
      execFileSync('git', ['config', 'user.email', 'test@test.com'], { cwd: tmpMain, stdio: 'pipe' });
      execFileSync('git', ['config', 'user.name', 'Test'], { cwd: tmpMain, stdio: 'pipe' });
      fs.writeFileSync(path.join(tmpMain, 'README.md'), '# test\n');
      execFileSync('git', ['add', '.'], { cwd: tmpMain, stdio: 'pipe' });
      execFileSync('git', ['commit', '-m', 'init'], { cwd: tmpMain, stdio: 'pipe' });
      execFileSync(BIN, ['init'], { cwd: tmpMain, timeout: 30_000, stdio: 'pipe' });

      execFileSync('git', ['worktree', 'add', tmpWt, '-b', 'test-wt-branch', 'HEAD'], { cwd: tmpMain, stdio: 'pipe' });

      const output = execFileSync(BIN, ['init'], { cwd: tmpWt, encoding: 'utf-8', timeout: 30_000 });
      expect(output.toLowerCase()).toMatch(/worktree|redirect/);
      expect(fs.existsSync(path.join(tmpWt, '.thrum', 'redirect'))).toBe(true);
    } finally {
      try { execFileSync(BIN, ['daemon', 'stop'], { cwd: tmpMain, timeout: 5_000, stdio: 'pipe' }); } catch { /* ok */ }
      try { execFileSync('git', ['worktree', 'remove', tmpWt, '--force'], { cwd: tmpMain, stdio: 'pipe' }); } catch { /* ok */ }
      fs.rmSync(tmpMain, { recursive: true, force: true });
      fs.rmSync(tmpWt, { recursive: true, force: true });
    }
  });

  test('J4: Init does NOT write mcpServers to settings.json', async () => {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'thrum-mcp-test-'));
    try {
      execFileSync('git', ['init'], { cwd: tmpDir, stdio: 'pipe' });
      execFileSync('git', ['config', 'user.email', 'test@test.com'], { cwd: tmpDir, stdio: 'pipe' });
      execFileSync('git', ['config', 'user.name', 'Test'], { cwd: tmpDir, stdio: 'pipe' });
      fs.writeFileSync(path.join(tmpDir, 'README.md'), '# test\n');
      execFileSync('git', ['add', '.'], { cwd: tmpDir, stdio: 'pipe' });
      execFileSync('git', ['commit', '-m', 'init'], { cwd: tmpDir, stdio: 'pipe' });
      execFileSync(BIN, ['init'], { cwd: tmpDir, timeout: 30_000, stdio: 'pipe' });

      const settingsPath = path.join(tmpDir, '.claude', 'settings.json');
      if (fs.existsSync(settingsPath)) {
        const content = fs.readFileSync(settingsPath, 'utf-8');
        expect(content).not.toContain('mcpServers');
      }
    } finally {
      try { execFileSync(BIN, ['daemon', 'stop'], { cwd: tmpDir, timeout: 5_000, stdio: 'pipe' }); } catch { /* ok */ }
      fs.rmSync(tmpDir, { recursive: true, force: true });
    }
  });

  test('J5: Wait subscription cleanup on disconnect', async () => {
    let error = '';
    try {
      execFileSync(BIN, ['wait', '--timeout', '1s'], {
        cwd: getTestRoot(), encoding: 'utf-8', timeout: 10_000,
        env: regressionEnv(),
      });
    } catch { /* timeout is expected */ }

    try {
      execFileSync(BIN, ['wait', '--timeout', '1s'], {
        cwd: getTestRoot(), encoding: 'utf-8', timeout: 10_000,
        env: regressionEnv(),
      });
    } catch (err: any) {
      error = err.stderr?.toString() || err.message || '';
    }
    expect(error.toLowerCase()).not.toContain('subscription already exists');
  });

  test('J6: Ping resolves by agent name', async () => {
    // Ping self — e2e_regtest agent is guaranteed to exist (registered in beforeAll)
    let output = '';
    try {
      output = thrumIn(getTestRoot(), ['ping', '@e2e_regtest'], 10_000, regressionEnv());
    } catch (err: any) {
      output = err.message || '';
    }
    expect(output.toLowerCase()).toContain('regtest');
    expect(output.toLowerCase()).not.toContain('not found');
  });

  test('J7: MCP serve does not crash', async () => {
    try {
      execFileSync(BIN, ['mcp', 'serve'], {
        cwd: getTestRoot(), encoding: 'utf-8', timeout: 3_000,
        env: regressionEnv(),
      });
    } catch (err: any) {
      const stderr = err.stderr?.toString() || '';
      expect(stderr.toLowerCase()).not.toContain('panic');
      expect(stderr.toLowerCase()).not.toContain('nil pointer');
    }
  });

  test('J8: Wait --all flag removed', async () => {
    let error = '';
    try {
      execFileSync(BIN, ['wait', '--all', '--timeout', '1s'], {
        cwd: getTestRoot(), encoding: 'utf-8', timeout: 5_000,
        env: regressionEnv(),
      });
    } catch (err: any) {
      error = err.stderr?.toString() || err.message || '';
    }
    expect(error.toLowerCase()).toContain('unknown flag');
  });

  test('J9: Unknown recipient hard error with inbox verification', async () => {
    // Mark all read before the test
    thrumIn(getTestRoot(), ['message', 'read', '--all'], 10_000, regressionEnv());

    let error = '';
    try {
      thrumIn(getTestRoot(), ['send', 'fail', '--to', '@does-not-exist'], 10_000, regressionEnv());
    } catch (err: any) {
      error = err.message || '';
    }
    expect(error.toLowerCase()).toContain('unknown');

    // Verify: failed send must not create a phantom message in our inbox
    const inbox = thrumIn(getTestRoot(), ['inbox', '--unread'], 10_000, regressionEnv());
    expect(inbox.toLowerCase()).not.toContain('fail');
  });

  test('J11: Group-send warning excludes @everyone', async () => {
    let output = '';
    try {
      output = thrumIn(getTestRoot(), ['send', 'Everyone message', '--to', '@everyone'], 10_000, regressionEnv());
    } catch (err: any) {
      output = err.message || '';
    }
    expect(output.toLowerCase()).not.toContain('resolved to a group');
  });

  test('J12: Wait excludes own outbound messages', async () => {
    thrumIn(getTestRoot(), ['send', 'Self-exclude test', '--to', '@everyone'], 10_000, regressionEnv());

    let waitOutput = '';
    try {
      waitOutput = execFileSync(BIN, ['wait', '--after', '-5s', '--timeout', '2s'], {
        cwd: getTestRoot(), encoding: 'utf-8', timeout: 10_000,
        env: regressionEnv(),
      });
    } catch (err: any) {
      waitOutput = err.stdout?.toString() || '';
    }
    expect(waitOutput).not.toContain('Self-exclude test');
  });

  test('J10: Name-only routing and auto role group warning', async () => {
    // Register a temp agent with a distinct name and role
    const j10Env = { ...process.env, THRUM_NAME: 'j10_alpha', THRUM_ROLE: 'j10_worker', THRUM_MODULE: 'all' };
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'j10_worker', '--module', 'all',
        '--name', 'j10_alpha', '--intent', 'Routing test'], 10_000, j10Env);
    } catch { /* may exist */ }

    // Sending to @j10_alpha by name → no "resolved to a group" warning
    const byNameResult = spawnSync(BIN, ['send', 'Direct to alpha', '--to', '@j10_alpha'], {
      cwd: getTestRoot(), encoding: 'utf-8', timeout: 10_000, env: regressionEnv(),
    });
    const byNameAll = `${byNameResult.stdout ?? ''}${byNameResult.stderr ?? ''}`;
    expect(byNameAll).not.toContain('resolved to a group');

    // Sending to @j10_worker by role → auto-group routing, expect "resolved to a group" warning
    // Warning goes to stderr; spawnSync captures stdout and stderr separately
    const byRoleResult = spawnSync(BIN, ['send', 'To worker role', '--to', '@j10_worker'], {
      cwd: getTestRoot(), encoding: 'utf-8', timeout: 10_000, env: regressionEnv(),
    });
    const byRoleStderr = String(byRoleResult.stderr ?? '');
    expect(byRoleStderr).toContain('resolved to a group');
  });

  test('J13: Name cannot equal role', async () => {
    // Attempt: name equals own role → should error
    const j13Env = { ...process.env, THRUM_NAME: 'j13_reviewer', THRUM_ROLE: 'j13_reviewer', THRUM_MODULE: 'all' };
    let error1 = '';
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'j13_reviewer', '--module', 'all',
        '--name', 'j13_reviewer', '--intent', 'Should fail'], 10_000, j13Env);
    } catch (err: any) {
      error1 = err.message || '';
    }
    expect(error1.toLowerCase()).toMatch(/name.*cannot.*same.*role|name.*role/);

    // Re-registration with different name+role → OK (no error)
    const j13OkEnv = { ...process.env, THRUM_NAME: 'j13_gamma', THRUM_ROLE: 'j13_planner', THRUM_MODULE: 'all' };
    let reregError = '';
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'j13_planner', '--module', 'all',
        '--name', 'j13_gamma', '--intent', 'Distinct name OK'], 10_000, j13OkEnv);
    } catch (err: any) {
      reregError = err.message || '';
    }
    expect(reregError).not.toMatch(/cannot.*same.*role/i);
  });
});

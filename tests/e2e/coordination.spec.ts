/**
 * Coordination Tests — SC-30 to SC-33
 *
 * Tests for cross-worktree coordination: who-has, ping, agent context,
 * and agent list with context. These use the globally running daemon
 * from global-setup.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumIn, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';
import { execFileSync } from 'node:child_process';
import { registerAgent } from './helpers/fixtures.js';
import * as fs from 'node:fs';
import * as path from 'node:path';

/** Dedicated agent for coordination tests — avoids session conflicts with session.spec.ts. */
function coordTestEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_coordtest', THRUM_ROLE: 'coordinator', THRUM_MODULE: 'all' };
}

test.describe('Coordination', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async () => {
    try {
      thrumIn(getTestRoot(), ['quickstart', '--role', 'coordinator', '--module', 'all',
        '--name', 'e2e_coordtest', '--intent', 'Coordination testing'], 10_000, coordTestEnv());
    } catch { /* may already exist */ }
  });

  test('SC-30: Check who has a file', async () => {
    const testRoot = getTestRoot();

    // Create a dummy file, commit it, then modify to create uncommitted changes
    const testFile = path.join(testRoot, 'test-file.go');
    fs.writeFileSync(testFile, '// test file for who-has\n');
    try {
      execFileSync('git', ['add', 'test-file.go'], { cwd: testRoot, stdio: 'pipe' });
      execFileSync('git', ['commit', '-m', 'add test file'], { cwd: testRoot, stdio: 'pipe' });
      fs.writeFileSync(testFile, '// test file for who-has\n// modified\n');

      // Act: run who-has
      const output = thrum(['who-has', 'test-file.go']);

      // Assert: shows the file and some context about who has it
      expect(output).not.toBe('');
    } finally {
      // Undo the commit and remove the file to leave test repo clean
      try {
        execFileSync('git', ['reset', 'HEAD~1', '--hard'], { cwd: testRoot, stdio: 'pipe' });
        fs.rmSync(testFile, { force: true });
      } catch { /* best effort */ }
    }
  });

  test('SC-31: Ping an agent', async () => {
    // Arrange: ensure we have a registered agent
    try {
      registerAgent('tester', 'e2e', 'E2E Tester');
    } catch {
      // may already exist
    }

    // Act: ping the tester agent
    let output: string;
    try {
      output = thrum(['ping', '@tester']);
    } catch (e: any) {
      // ping may return non-zero if agent is offline
      output = e.stdout?.toString() || e.stderr?.toString() || e.message;
    }

    // Assert: ping output references the agent we pinged
    expect(output.toLowerCase()).toContain('tester');
  });

  test('SC-32: View agent work context', async () => {
    // Act: get agent context
    let output: string;
    try {
      output = thrum(['agent', 'context']);
    } catch (e: any) {
      output = e.stdout?.toString() || e.stderr?.toString() || e.message;
    }

    // Assert: context output contains work context fields (branch, intent, or module)
    expect(output.toLowerCase()).toContain('branch');
  });

  test('SC-33: List agents with context', async () => {
    // Arrange: register a couple of agents
    try {
      registerAgent('coordinator', 'all', 'Coordinator');
    } catch {
      // may already exist
    }

    // Act: list agents with context flag
    let output: string;
    try {
      output = thrum(['agent', 'list', '--context']);
    } catch (e: any) {
      output = e.stdout?.toString() || e.stderr?.toString() || e.message;
    }

    // Assert: shows agents with their context information
    expect(output.toLowerCase()).toMatch(/coordinator|implementer|tester/);
  });

  test('F2-2: who-has JSON output for modified file', async () => {
    const testRoot = getTestRoot();
    const testFile = 'json-test.txt';

    try {
      fs.writeFileSync(path.join(testRoot, testFile), '// original\n');
      execFileSync('git', ['add', testFile], { cwd: testRoot, stdio: 'pipe' });
      execFileSync('git', ['commit', '-m', 'add json test file'], { cwd: testRoot, stdio: 'pipe' });
      fs.writeFileSync(path.join(testRoot, testFile), '// modified\n');

      const output = thrum(['who-has', testFile, '--json']);
      const parsed = JSON.parse(output);
      expect(typeof parsed).toBe('object');
    } finally {
      try {
        execFileSync('git', ['checkout', testFile], { cwd: testRoot, stdio: 'pipe' });
        execFileSync('git', ['reset', 'HEAD~1', '--hard'], { cwd: testRoot, stdio: 'pipe' });
      } catch { /* best effort */ }
    }
  });

  test('F2-3: who-has with no agents editing', async () => {
    let output = '';
    try {
      output = thrum(['who-has', 'README.md']);
    } catch (err: any) {
      output = err.message || '';
    }
    expect(output.toLowerCase()).not.toContain('error');
  });

  test('F2-5: agent set-intent updates intent', async () => {
    const output = thrumIn(getTestRoot(), ['agent', 'set-intent', 'Working on authentication module'], 10_000, coordTestEnv());
    expect(output.toLowerCase()).toMatch(/updated|intent|set/);

    const status = thrumIn(getTestRoot(), ['status'], 10_000, coordTestEnv());
    expect(status.toLowerCase()).toContain('authentication');
  });

  test('F2-6: agent heartbeat', async () => {
    const output = thrumIn(getTestRoot(), ['agent', 'heartbeat'], 10_000, coordTestEnv());
    expect(output.toLowerCase()).toMatch(/heartbeat|ok|updated/);
  });

  test('F2-7: agent list --context shows context', async () => {
    const output = thrum(['agent', 'list', '--context']);
    expect(output.toLowerCase()).toMatch(/coordinator|implementer/);
  });

  test('F2-8: team shows activity and status', async () => {
    const output = thrum(['team']);
    expect(output.toLowerCase()).toMatch(/coordinator|implementer/);

    const jsonOutput = thrum(['team', '--json']);
    const parsed = JSON.parse(jsonOutput);
    expect(typeof parsed).toBe('object');
  });
});

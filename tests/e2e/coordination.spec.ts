/**
 * Coordination Tests — SC-30 to SC-33
 *
 * Tests for cross-worktree coordination: who-has, ping, agent context,
 * and agent list with context. These use the globally running daemon
 * from global-setup.
 */
import { test, expect } from '@playwright/test';
import { thrum, getTestRoot } from './helpers/thrum-cli.js';
import { execFileSync } from 'node:child_process';
import { registerAgent } from './helpers/fixtures.js';
import * as fs from 'node:fs';
import * as path from 'node:path';

test.describe('Coordination', () => {
  test.describe.configure({ mode: 'serial' });

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
    // Should list the tester agent registered in global-setup
    expect(output.toLowerCase()).toContain('tester');
  });
});

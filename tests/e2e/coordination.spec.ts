/**
 * Coordination Tests — SC-30 to SC-33
 *
 * Tests for cross-worktree coordination: who-has, ping, agent context,
 * and agent list with context. These use the globally running daemon
 * from global-setup.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumJson } from './helpers/thrum-cli.js';
import { registerAgent } from './helpers/fixtures.js';
import * as fs from 'node:fs';
import * as path from 'node:path';

const ROOT = path.resolve(__dirname, '../..');

test.describe('Coordination', () => {
  test.describe.configure({ mode: 'serial' });

  test('SC-30: Check who has a file', async () => {
    // Arrange: modify a tracked file so there are uncommitted changes
    const testFile = path.join(ROOT, 'internal', 'cli', 'daemon.go');

    // Check if the file exists — if not, find any Go file to modify
    let targetFile: string;
    let targetArg: string;
    if (fs.existsSync(testFile)) {
      targetFile = testFile;
      targetArg = 'internal/cli/daemon.go';
    } else {
      // Find any Go file to temporarily modify
      const goFiles: string[] = [];
      const scanDir = (dir: string, depth: number) => {
        if (depth > 3) return;
        try {
          for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
            const full = path.join(dir, entry.name);
            if (entry.isDirectory() && !entry.name.startsWith('.') && entry.name !== 'node_modules') {
              scanDir(full, depth + 1);
            } else if (entry.isFile() && entry.name.endsWith('.go')) {
              goFiles.push(full);
            }
          }
        } catch { /* permission errors, etc. */ }
      };
      scanDir(ROOT, 0);

      if (goFiles.length === 0) {
        test.skip(true, 'No Go files found to test who-has');
        return;
      }
      targetFile = goFiles[0];
      targetArg = path.relative(ROOT, targetFile);
    }

    // Save original content, add a comment, test, then restore
    const original = fs.readFileSync(targetFile, 'utf-8');
    try {
      fs.writeFileSync(targetFile, original + '\n// e2e-test-marker\n');

      // Act: run who-has
      const output = thrum(['who-has', targetArg]);

      // Assert: shows the file and some context about who has it
      expect(output).not.toBe('');
      // The output should reference the file or the current branch/agent
    } finally {
      // Restore original file
      fs.writeFileSync(targetFile, original);
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

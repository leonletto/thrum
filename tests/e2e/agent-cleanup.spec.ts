import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { thrum, getTestRoot } from './helpers/thrum-cli.js';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

/**
 * Helper to run thrum commands that may fail, returning { stdout, stderr, exitCode }.
 */
function thrumRaw(
  args: string[],
  env?: NodeJS.ProcessEnv,
): { stdout: string; stderr: string; exitCode: number } {
  try {
    // Prepend --repo to anchor identity resolution to the test's worktree,
    // bypassing the strict cross_worktree guard (v0.10.4+) when the test
    // process's pane has a different agent identity than the test root.
    const stdout = execFileSync(BIN, ['--repo', getTestRoot(), ...args], {
      cwd: getTestRoot(),
      encoding: 'utf-8',
      timeout: 10_000,
      env: env ?? {
        ...process.env,
        THRUM_ROLE: 'tester',
        THRUM_MODULE: 'e2e',
      },
    }).trim();
    return { stdout, stderr: '', exitCode: 0 };
  } catch (err: any) {
    // Stderr includes the captured error output even when our --repo prepend
    // changes the resolved repo path; tests verify exitCode + stderr content.
    return {
      stdout: err.stdout?.toString().trim() || '',
      stderr: err.stderr?.toString().trim() || '',
      exitCode: err.status ?? 1,
    };
  }
}

/**
 * Resolve the sync worktree path for reading events.jsonl.
 */
function getSyncDir(): string {
  const testRoot = getTestRoot();
  const output = execFileSync('git', ['-C', testRoot, 'rev-parse', '--git-common-dir'], {
    encoding: 'utf-8',
  }).trim();
  const gitCommonDir = path.isAbsolute(output) ? output : path.join(testRoot, output);
  return path.join(gitCommonDir, 'thrum-sync', 'a-sync');
}

test.describe('Agent Cleanup Tests', () => {
  test('AN-10: Agent delete removes all artifacts', async () => {
    const agentName = `cleanup_target_${Date.now()}`;

    // Register agent
    thrum([
      'agent', 'register',
      '--name', agentName,
      '--role', 'tester',
      '--module', 'cleanup-test',
      '--force',
    ]);

    // Start session so we can send messages
    thrum(['session', 'start'], 10_000, {
      ...process.env,
      THRUM_NAME: agentName,
      THRUM_ROLE: 'tester',
      THRUM_MODULE: 'cleanup-test',
    });

    // Send a message from this agent (thrum-t698: explicit --broadcast;
    // the test agent is ephemeral and may be the only registered agent
    // in this test's env, so --to @<directed> isn't always available)
    thrum(['send', `Test message from ${agentName}`, '--broadcast'], 10_000, {
      ...process.env,
      THRUM_NAME: agentName,
      THRUM_ROLE: 'tester',
      THRUM_MODULE: 'cleanup-test',
    });

    // Verify agent exists in list
    const beforeResult = thrumRaw(['agent', 'list', '--json']);
    expect(beforeResult.stdout).toContain(agentName);

    // Delete agent (with --force to skip confirmation)
    const deleteResult = thrumRaw(['agent', 'delete', agentName, '--force']);
    expect(deleteResult.exitCode).toBe(0);

    // Verify identity file removed
    const identityPath = path.join(getTestRoot(), '.thrum', 'identities', `${agentName}.json`);
    expect(fs.existsSync(identityPath)).toBe(false);

    // Verify message file removed
    const syncDir = getSyncDir();
    const messagePath = path.join(syncDir, 'messages', `${agentName}.jsonl`);
    expect(fs.existsSync(messagePath)).toBe(false);

    // Verify agent no longer in agent list
    const afterResult = thrumRaw(['agent', 'list', '--json']);
    expect(afterResult.stdout).not.toContain(`"${agentName}"`);

    // Verify events.jsonl contains agent.cleanup event.
    // Per v0.10.6 sync-rearchitect (state.go:96-100), the canonical event
    // journal lives at <repo>/.thrum/events.jsonl (LOCAL, not synced). The
    // sync worktree's events.jsonl is legacy/read-fallback only — no new
    // events written there (merge.go:84,910). Read the local journal.
    const eventsPath = path.join(getTestRoot(), '.thrum', 'events.jsonl');
    const events = fs.readFileSync(eventsPath, 'utf-8');
    const cleanupEvents = events
      .split('\n')
      .filter(line => line.trim())
      .map(line => { try { return JSON.parse(line); } catch { return null; } })
      .filter(e => e && e.type === 'agent.cleanup' && e.agent_id === agentName);
    expect(cleanupEvents.length).toBeGreaterThan(0);
  });

  test('AN-11: Delete non-existent agent returns error', async () => {
    const result = thrumRaw(['agent', 'delete', 'nonexistent_agent_xyz', '--force']);

    // Should return non-zero exit code
    expect(result.exitCode).not.toBe(0);

    // Should contain "not found" error message
    const combined = (result.stdout + result.stderr).toLowerCase();
    expect(combined).toMatch(/not found|does not exist/);
  });

  test('AN-15: --force and --dry-run are mutually exclusive', async () => {
    const result = thrumRaw(['agent', 'cleanup', '--force', '--dry-run']);

    // Should return non-zero exit code
    expect(result.exitCode).not.toBe(0);

    // Should indicate flags are mutually exclusive
    const combined = (result.stdout + result.stderr).toLowerCase();
    expect(combined).toMatch(/mutually exclusive/);
  });

  test('AN-14: Agent cleanup emits event in events.jsonl', async () => {
    const agentName = `audit_target_${Date.now()}`;

    // Register agent
    thrum([
      'agent', 'register',
      '--name', agentName,
      '--role', 'tester',
      '--module', 'audit-test',
      '--force',
    ]);

    // Delete agent
    const deleteResult = thrumRaw(['agent', 'delete', agentName, '--force']);
    expect(deleteResult.exitCode).toBe(0);

    // Read events.jsonl and verify agent.cleanup event. Per v0.10.6
    // sync-rearchitect (state.go:96-100), the canonical event journal
    // is <repo>/.thrum/events.jsonl (LOCAL); sync-worktree path is
    // legacy/read-fallback only (merge.go:84,910).
    const eventsPath = path.join(getTestRoot(), '.thrum', 'events.jsonl');
    const events = fs.readFileSync(eventsPath, 'utf-8');

    const cleanupEvents = events
      .split('\n')
      .filter(line => line.trim())
      .map(line => { try { return JSON.parse(line); } catch { return null; } })
      .filter(e => e && e.type === 'agent.cleanup' && e.agent_id === agentName);

    expect(cleanupEvents.length).toBeGreaterThan(0);

    const event = cleanupEvents[0];
    // Verify required fields
    expect(event.timestamp).toBeTruthy();
    expect(event.agent_id).toBe(agentName);
    expect(event.method).toBe('manual');
    expect(event.reason).toBeTruthy();
  });
});

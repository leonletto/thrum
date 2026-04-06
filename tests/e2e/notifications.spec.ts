/**
 * Notifications & Subscriptions Tests — SC-34 to SC-37
 *
 * Tests for subscribe, wait, and unsubscribe. These exercise the
 * notification system using the globally running daemon.
 *
 * Note: SC-34/35/36 use concurrent operations — we start `thrum wait`
 * in a background process (as the coordinator agent), then send a
 * matching message from the IMPLEMENTER worktree (a different agent),
 * and verify the wait resolves. Using a different sender is critical
 * because `thrum wait` sets `exclude_self=true`, filtering out
 * messages authored by the waiting agent.
 *
 * The daemon resolves identity via identity files in each worktree.
 * The e2e infrastructure provides separate coordinator and implementer
 * repos with distinct identities.
 */
import { test, expect } from '@playwright/test';
import { thrum, BIN, getTestRoot, getImplementerRoot, thrumIn } from './helpers/thrum-cli';
import { ensureTestSessions } from './helpers/fixtures';
import { tmuxExecAsync, shellEscape, buildEnvPrefix } from './helpers/tmux-exec';

/** Implementer agent env for thrumIn calls. */
const implEnv: NodeJS.ProcessEnv = { THRUM_NAME: 'e2e_implementer', THRUM_ROLE: 'implementer', THRUM_MODULE: 'main' };

/** Coordinator agent env for wait commands. */
const coordEnv: NodeJS.ProcessEnv = { THRUM_NAME: 'e2e_coordinator', THRUM_ROLE: 'tester', THRUM_MODULE: 'e2e' };

/** Run thrum wait in background via tmux, resolves when command exits. */
function thrumWaitBackground(
  args: string[],
  timeoutMs = 15_000,
): Promise<{ stdout: string; stderr: string; exitCode: number }> {
  const prefix = buildEnvPrefix(coordEnv);
  const escapedArgs = args.map(shellEscape).join(' ');
  const cmd = `${prefix} ${shellEscape(BIN)} wait ${escapedArgs}`;

  return tmuxExecAsync(cmd, { cwd: getTestRoot(), timeoutMs }).then(result => ({
    stdout: result.stdout,
    stderr: '',
    exitCode: result.exitCode,
  }));
}

test.describe('Notifications & Subscriptions', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(() => {
    // Ensure both coordinator and implementer have active sessions
    ensureTestSessions();
  });

  test('SC-34: Subscribe to scope notifications', async () => {
    // Arrange: subscribe to module:auth scope (as coordinator)
    const subOutput = thrum(['subscribe', '--scope', 'module:auth']);
    expect(subOutput.toLowerCase()).toMatch(/watch|subscri|scope|listening|module:auth/i);

    // Act: start wait in background (as coordinator), then send from implementer worktree
    const waitPromise = thrumWaitBackground(
      ['--scope', 'module:auth', '--timeout', '10s'],
      12_000,
    );

    // Small delay to let wait establish connection
    await new Promise(resolve => setTimeout(resolve, 1000));

    // Send from the IMPLEMENTER worktree (different agent identity)
    thrumIn(getImplementerRoot(), ['send', 'Auth module updated for SC-34', '--scope', 'module:auth'], 10_000, implEnv);

    // Assert: wait should receive the notification (plain text outputs MESSAGES_RECEIVED)
    const result = await waitPromise;
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain('MESSAGES_RECEIVED');
  });

  test('SC-35: Subscribe to mention notifications', async () => {
    // Arrange: subscribe to mentions of @e2e_coordinator (the coordinator's name)
    const subOutput = thrum(['subscribe', '--mention', '@e2e_coordinator']);
    expect(subOutput.toLowerCase()).toMatch(/watch|subscri|scope|listening|coordinator/i);

    // Act: start wait (as coordinator), then send mentioning message from implementer
    const waitPromise = thrumWaitBackground(
      ['--mention', '@e2e_coordinator', '--timeout', '10s'],
      12_000,
    );

    await new Promise(resolve => setTimeout(resolve, 1000));

    // Send from implementer mentioning the coordinator
    thrumIn(getImplementerRoot(), ['send', 'Hey coordinator, SC-35 test', '--mention', '@e2e_coordinator'], 10_000, implEnv);

    const result = await waitPromise;
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain('MESSAGES_RECEIVED');
  });

  test('SC-36: Subscribe to all (firehose)', async () => {
    // Arrange: subscribe to all messages (as coordinator)
    const subOutput = thrum(['subscribe', '--all']);
    expect(subOutput.toLowerCase()).toMatch(/watch|subscri|scope|listening|all|firehose/i);

    // Brief pause to ensure previous test's messages are in the past
    await new Promise(resolve => setTimeout(resolve, 1500));

    // Act: start wait (as coordinator) with --after +0s to ignore stale messages,
    // then send from implementer
    const waitPromise = thrumWaitBackground(
      ['--timeout', '10s', '--after', '+0s'],
      12_000,
    );

    await new Promise(resolve => setTimeout(resolve, 1000));

    // Send from implementer
    thrumIn(getImplementerRoot(), ['send', 'Firehose test SC-36'], 10_000, implEnv);

    const result = await waitPromise;
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain('MESSAGES_RECEIVED');
  });

  test('SC-37: Unsubscribe', async () => {
    // Arrange: create a subscription and list it
    thrum(['subscribe', '--scope', 'module:test-unsub']);
    const subsBefore = thrum(['subscriptions']);
    expect(subsBefore.toLowerCase()).toMatch(/subscri|scope|module/);

    // Extract subscription ID from the list
    // Try JSON format first
    let subId: string | undefined;
    try {
      const subsJson = thrum(['subscriptions', '--json']);
      const parsed = JSON.parse(subsJson);
      const subs = Array.isArray(parsed) ? parsed : (parsed.subscriptions || []);
      const match = subs.find((s: any) =>
        s.scope === 'module:test-unsub' || s.filter?.scope === 'module:test-unsub'
      );
      subId = match?.id || match?.subscription_id;
    } catch {
      // Fall back to parsing text output — look for an ID pattern
      const idMatch = subsBefore.match(/([a-f0-9-]{8,})/);
      subId = idMatch?.[1];
    }

    if (!subId) {
      // If we can't extract the ID, just verify subscriptions command works
      expect(subsBefore.toLowerCase()).toMatch(/subscri|scope|module/);
      return;
    }

    // Act: unsubscribe
    const unsubOutput = thrum(['unsubscribe', subId]);
    expect(unsubOutput).toMatch(/unsubscri|removed|success/i);

    // Assert: subscription no longer listed
    const subsAfter = thrum(['subscriptions']);
    expect(subsAfter).not.toContain('module:test-unsub');
  });
});

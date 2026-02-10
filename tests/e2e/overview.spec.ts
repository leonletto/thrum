import { test, expect } from '@playwright/test';
import { thrum, thrumJson } from './helpers/thrum-cli.js';

test.describe.serial('Overview & Status Tests', () => {
  test.beforeEach(async () => {
    // Ensure we have an active session
    try {
      thrum(['session', 'start']);
    } catch (err: any) {
      const msg = err.stderr?.toString() || err.message || '';
      if (!msg.toLowerCase().includes('already active') && !msg.toLowerCase().includes('already exists')) {
        throw err;
      }
    }
  });

  test.afterAll(async () => {
    try { thrum(['session', 'end']); } catch { /* session may already be ended */ }
  });

  test('SC-65: Combined overview', async () => {
    // Arrange: send some messages for the overview to show
    thrum(['send', 'Test message for overview']);

    // Act: run overview command
    const overview = thrum(['overview']);

    // Assert: overview contains key information
    expect(overview.length).toBeGreaterThan(0);
    // Overview should show agent info, messages, and daemon status
  });

  test('SC-66: Agent status', async () => {
    // Act: run status command
    const status = thrum(['status']);

    // Assert: status contains agent identity
    expect(status.toLowerCase()).toContain('agent');
    // Status should show role, module, session info
  });

  test.fixme('SC-67: JSON output mode - --json flag not yet supported for all commands', async () => {
    // Act: run commands with --json flag
    const statusJson = thrumJson(['status']);
    const inboxJson = thrumJson(['inbox']);
    const agentListJson = thrumJson(['agent', 'list']);

    // Assert: all outputs are valid JSON (thrumJson would throw if not)
    expect(statusJson).not.toBeNull();
    expect(inboxJson).not.toBeNull();
    expect(agentListJson).not.toBeNull();

    // Assert: JSON outputs are objects/arrays with required fields
    expect(typeof statusJson).toBe('object');
    expect(statusJson).toHaveProperty('agent_id');
    expect(statusJson).toHaveProperty('role');

    expect(Array.isArray(inboxJson) || typeof inboxJson === 'object').toBe(true);
    if (Array.isArray(inboxJson) && inboxJson.length > 0) {
      expect(inboxJson[0]).toHaveProperty('id');
      expect(inboxJson[0]).toHaveProperty('text');
    }

    expect(Array.isArray(agentListJson) || typeof agentListJson === 'object').toBe(true);
  });
});

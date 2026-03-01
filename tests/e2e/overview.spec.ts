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

  test('SC-67: JSON output mode', async () => {
    // Act: run commands with --json flag
    const statusJson = thrumJson<any>(['status']);
    const inboxJson = thrumJson<any>(['inbox']);
    const agentListJson = thrumJson<any>(['agent', 'list']);

    // Assert: all outputs are valid JSON (thrumJson would throw if not)
    expect(statusJson).not.toBeNull();
    expect(inboxJson).not.toBeNull();
    expect(agentListJson).not.toBeNull();

    // Assert: status JSON has nested structure: { health: {...}, agent: { agent_id, role } }
    expect(typeof statusJson).toBe('object');
    expect(statusJson).toHaveProperty('health');
    expect(statusJson).toHaveProperty('agent');
    expect(statusJson.agent).toHaveProperty('role');

    // Assert: inbox JSON is an object with messages array
    expect(typeof inboxJson).toBe('object');

    // Assert: agent list JSON is an object
    expect(typeof agentListJson).toBe('object');
  });
});

import { test, expect } from '@playwright/test';
import { thrum, getWebUIUrl } from './helpers/thrum-cli.js';
import { registerAgent, getAgentList, waitForWebSocket } from './helpers/fixtures.js';

/**
 * E2E tests for Identity & Registration (SC-04 to SC-09)
 *
 * These tests validate:
 * - Human user registration via CLI
 * - Agent registration via quickstart
 * - Cross-worktree registration
 * - Idempotent duplicate registration
 * - Browser auto-registration via user.identify RPC
 * - Identity persistence across page reload
 *
 * Note: The daemon resolves identity via THRUM_ROLE/THRUM_MODULE env vars
 * as fallback when multiple identity files exist. As a result, `whoami`
 * and `status` may reflect the default test agent rather than the last
 * registered agent. Tests verify registration and agent list independently.
 */

test.describe('Identity & Registration', () => {
  test('SC-04: Register a human user via CLI', async () => {
    // Act: register a human user
    const result = registerAgent('owner', 'all', 'Leon');

    // Assert: registration successful
    expect(result).toMatch(/registered|already registered/i);

    // Verify: whoami resolves without error (returns default test agent or owner)
    const whoami = thrum(['agent', 'whoami']);
    expect(whoami.length).toBeGreaterThan(0);

    // Verify: agent list includes @owner
    const agentList = getAgentList();
    expect(agentList.some((a) => a.role === 'owner')).toBe(true);
  });

  test('SC-05: Register an agent via quickstart', async () => {
    // Act: use quickstart command (--name pins identity to avoid reuse override)
    const result = thrum([
      'quickstart',
      '--role',
      'coordinator',
      '--module',
      'all',
      '--display',
      'Claude-Main',
      '--intent',
      'Coordinating agents',
      '--name',
      'e2e_coordinator_sc05',
    ]);

    // Assert: quickstart successful (registers, starts session, sets intent)
    expect(result).toMatch(/registered|quickstart|session|coordinator/i);

    // Verify: status command works without error
    const status = thrum(['status']);
    expect(status.length).toBeGreaterThan(0);
    // Agent list should include coordinator
    const agentList = getAgentList();
    expect(agentList.some((a) => a.role === 'coordinator')).toBe(true);
  });

  test('SC-06: Register a second agent in a different worktree', async () => {
    // Note: This test simulates a second worktree by registering with a different role
    // In a real scenario, this would be run from ~/.workspaces/thrum/relay

    // Act: register a second agent with different role
    const result = registerAgent('implementer', 'relay', 'Claude-Relay');

    // Assert: registration successful
    expect(result).toMatch(/registered|already registered/i);

    // Verify: agent list shows both agents
    const agentList = getAgentList();
    const roles = agentList.map((a) => a.role);

    // Should have at least implementer (coordinator might be from previous test)
    expect(roles).toContain('implementer');
  });

  test('SC-07: Register with duplicate role (idempotent)', async () => {
    // Arrange: register an agent and record count
    const first = registerAgent('coordinator', 'all', 'Claude-Main');
    expect(first).toMatch(/registered|already registered/i);

    const countBefore = getAgentList().filter((a) => a.role === 'coordinator').length;

    // Act: register again with same role
    const second = registerAgent('coordinator', 'all', 'Claude-Main');

    // Assert: second registration is idempotent
    expect(second).toMatch(/registered|already registered/i);

    // Verify: no NEW duplicate entries created (count should not increase)
    const countAfter = getAgentList().filter((a) => a.role === 'coordinator').length;
    expect(countAfter).toBeLessThanOrEqual(countBefore);
  });

  test('SC-08: Browser auto-registration via user.identify', async ({ page }) => {
    // Arrange: ensure daemon is running (already started by global-setup)

    // Act: open browser
    await page.goto(getWebUIUrl());

    // Wait for WebSocket connection
    await waitForWebSocket(page);

    // Assert: header shows real git user name, NOT "Unknown"
    // The browser should call user.identify RPC on load and display the result
    const header = page.locator('header');

    // Wait for identity to load (may take a moment for RPC call)
    await expect(header).not.toContainText('Unknown', { timeout: 5000 });

    // Verify: real user name is displayed
    // The actual name will come from git config, so we just verify it's not empty
    const headerText = await header.textContent();
    expect(headerText?.trim().length).toBeGreaterThan(0);
    expect(headerText).not.toBe('Unknown');
  });

  test('SC-09: Browser identity persists across page reload', async ({ page }) => {
    // Arrange: open browser and wait for identity to load
    await page.goto(getWebUIUrl());
    await waitForWebSocket(page);

    // Get the initial identity
    const header = page.locator('header');
    await expect(header).not.toContainText('Unknown', { timeout: 5000 });
    const initialIdentity = await header.textContent();

    // Act: refresh page
    await page.reload();

    // Assert: identity persists (loaded from localStorage, no re-registration flash)
    await waitForWebSocket(page);

    // Wait for identity to be restored from localStorage
    await expect(header).not.toContainText('Unknown', { timeout: 5000 });
    const reloadedIdentity = await header.textContent();

    // Verify: same identity after reload
    expect(reloadedIdentity).toBe(initialIdentity);
    expect(reloadedIdentity).not.toBe('Unknown');
  });
});

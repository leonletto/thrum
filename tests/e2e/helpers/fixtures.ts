import { type Page, expect } from '@playwright/test';
import { thrum, thrumJson, thrumIn, getTestRoot, getImplementerRoot } from './thrum-cli.js';

/**
 * Register an agent via CLI.
 * Uses --force to handle conflicts from prior test runs.
 *
 * @param name - optional agent name (creates .thrum/identities/{name}.json)
 */
export function registerAgent(
  role: string,
  module: string,
  display?: string,
  name?: string,
): string {
  const args = ['agent', 'register', '--role', role, '--module', module, '--force'];
  if (name) {
    args.push('--name', name);
  }
  if (display) {
    args.push('--display', display);
  }
  return thrum(args);
}

/**
 * Register an agent AND start a session via CLI quickstart.
 * This is needed for agents that need to send messages.
 *
 * @param name - optional agent name (creates .thrum/identities/{name}.json)
 */
export function quickstartAgent(
  role: string,
  module: string,
  display?: string,
  intent?: string,
  name?: string,
): string {
  const args = ['quickstart', '--role', role, '--module', module];
  if (name) {
    args.push('--name', name);
  }
  if (display) {
    args.push('--display', display);
  }
  if (intent) {
    args.push('--intent', intent);
  }
  return thrum(args);
}

/**
 * Send a message via CLI.
 */
export function sendMessage(
  text: string,
  options?: { to?: string; mention?: string },
): string {
  const args = ['send', text];
  if (options?.to) {
    args.push('--to', options.to);
  }
  if (options?.mention) {
    args.push('--mention', options.mention);
  }
  return thrum(args);
}

/**
 * Get inbox messages as parsed JSON.
 */
export function getInbox(): unknown[] {
  return thrumJson<unknown[]>(['inbox']);
}

/**
 * Get agent list as parsed JSON.
 * Returns array of agent objects.
 */
export function getAgentList(): Array<{ role: string; agent_id: string }> {
  const response = thrumJson<{ agents: { agents: Array<{ role: string; agent_id: string }> } }>([
    'agent',
    'list',
  ]);
  // Handle nested structure when contexts are included
  if (response.agents && 'agents' in response.agents) {
    return response.agents.agents;
  }
  // Fallback if structure is simpler
  return (response as any).agents || [];
}

/**
 * Wait for the WebSocket connection indicator in the UI.
 * The health bar shows "CONNECTED" when the WS link is up.
 */
export async function waitForWebSocket(page: Page): Promise<void> {
  await expect(page.getByText('CONNECTED')).toBeVisible({ timeout: 15_000 });
}

/**
 * Register an agent in a specific worktree via quickstart.
 */
export function quickstartAgentIn(
  cwd: string,
  role: string,
  module: string,
  name: string,
  intent?: string,
): string {
  const args = ['quickstart', '--role', role, '--module', module, '--name', name];
  if (intent) args.push('--intent', intent);
  return thrumIn(cwd, args);
}

/**
 * Ensure both coordinator and implementer have active sessions.
 * Useful as a beforeAll hook.
 */
export function ensureTestSessions(): void {
  try {
    thrumIn(getTestRoot(), ['session', 'start']);
  } catch (err: any) {
    if (!err.message?.toLowerCase().includes('already')) throw err;
  }
  try {
    thrumIn(getImplementerRoot(), ['session', 'start']);
  } catch (err: any) {
    if (!err.message?.toLowerCase().includes('already')) throw err;
  }
}

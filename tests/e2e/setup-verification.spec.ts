/**
 * Setup Verification Tests — D1 to D4
 *
 * Verify that the global-setup correctly created the test environment:
 * coordinator repo, implementer worktree with redirect, registered agents,
 * and team roster. These tests run first (serial) to confirm the E2E
 * infrastructure before other specs exercise features.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumIn, thrumJson, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';
import * as fs from 'node:fs';
import * as path from 'node:path';

/** Coordinator agent identity. */
function coordEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_coordinator', THRUM_ROLE: 'coordinator', THRUM_MODULE: 'all' };
}

/** Implementer agent identity. */
function implEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_implementer', THRUM_ROLE: 'implementer', THRUM_MODULE: 'main' };
}

test.describe('Setup Verification', () => {
  test.describe.configure({ mode: 'serial' });

  test('D1: Coordinator and implementer worktrees exist with redirect', async () => {
    const testRoot = getTestRoot();
    const implRoot = getImplementerRoot();

    // Coordinator repo exists and has .thrum/
    expect(fs.existsSync(testRoot)).toBe(true);
    expect(fs.existsSync(path.join(testRoot, '.thrum'))).toBe(true);
    expect(fs.existsSync(path.join(testRoot, '.thrum', 'identities'))).toBe(true);

    // Implementer worktree exists and has .thrum/redirect
    expect(fs.existsSync(implRoot)).toBe(true);
    const redirectPath = path.join(implRoot, '.thrum', 'redirect');
    expect(fs.existsSync(redirectPath)).toBe(true);

    // Redirect points back to coordinator's .thrum/
    const redirectTarget = fs.readFileSync(redirectPath, 'utf-8').trim();
    expect(redirectTarget).toBe(path.join(testRoot, '.thrum'));

    // Implementer is a git worktree (has .git file, not .git directory)
    const implGitPath = path.join(implRoot, '.git');
    expect(fs.existsSync(implGitPath)).toBe(true);
    const gitStat = fs.statSync(implGitPath);
    expect(gitStat.isFile()).toBe(true); // worktrees have a .git file, not directory
  });

  test('D2: Coordinator agent is registered with active session', async () => {
    const testRoot = getTestRoot();

    // whoami from coordinator worktree resolves to e2e_coordinator
    const whoami = thrumIn(testRoot, ['agent', 'whoami', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(whoami);
    expect(parsed.name || parsed.agent_id).toMatch(/e2e_coordinator/);

    // Status shows agent info
    const status = thrumIn(testRoot, ['status'], 10_000, coordEnv());
    expect(status.toLowerCase()).toContain('coordinator');
  });

  test('D3: Implementer agent is registered with active session', async () => {
    const implRoot = getImplementerRoot();

    // whoami from implementer worktree resolves to e2e_implementer
    const whoami = thrumIn(implRoot, ['agent', 'whoami', '--json'], 10_000, implEnv());
    const parsed = JSON.parse(whoami);
    expect(parsed.name || parsed.agent_id).toMatch(/e2e_implementer/);

    // Status shows agent info
    const status = thrumIn(implRoot, ['status'], 10_000, implEnv());
    expect(status.toLowerCase()).toContain('implementer');
  });

  test('D4: Team roster shows both agents', async () => {
    const testRoot = getTestRoot();

    // Agent list from coordinator worktree should contain both agents
    // Agent list displays role names (@coordinator, @implementer), not identity file names
    const agentList = thrumIn(testRoot, ['agent', 'list'], 10_000, coordEnv());
    expect(agentList).toContain('@coordinator');
    expect(agentList).toContain('@implementer');

    // JSON team output is a valid object
    const teamJson = thrumIn(testRoot, ['team', '--json'], 10_000, coordEnv());
    const parsed = JSON.parse(teamJson);
    expect(typeof parsed).toBe('object');
  });
});

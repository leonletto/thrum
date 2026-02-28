/**
 * Group Tests — F1–F5
 *
 * Tests group creation, member management, role-based membership,
 * group messaging, and nested group behavior.
 */
import { test, expect } from '@playwright/test';
import { thrum, thrumIn, getTestRoot, getImplementerRoot } from './helpers/thrum-cli.js';

function coordEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_coordinator', THRUM_ROLE: 'coordinator', THRUM_MODULE: 'all' };
}

function implEnv(): NodeJS.ProcessEnv {
  return { ...process.env, THRUM_NAME: 'e2e_implementer', THRUM_ROLE: 'implementer', THRUM_MODULE: 'main' };
}

test.describe('Groups', () => {
  test.describe.configure({ mode: 'serial' });

  test('F1: Create group', async () => {
    const output = thrum(['group', 'create', 'test-team']);
    expect(output.toLowerCase()).toMatch(/created|test-team/);

    const list = thrum(['group', 'list']);
    expect(list.toLowerCase()).toContain('test-team');
  });

  test('F2: Add members to group', async () => {
    thrum(['group', 'add', 'test-team', '@e2e_coordinator']);
    thrum(['group', 'add', 'test-team', '@e2e_implementer']);

    const list = thrum(['group', 'list']);
    expect(list.toLowerCase()).toContain('test-team');
    expect(list).toMatch(/2/);
  });

  test('F3: Add by role', async () => {
    thrum(['group', 'create', 'coordinators']);
    thrum(['group', 'add', 'coordinators', '--role', 'coordinator']);

    const list = thrum(['group', 'list']);
    expect(list.toLowerCase()).toContain('coordinators');
  });

  test('F4: Send group message', async () => {
    // Mark all read first
    thrumIn(getImplementerRoot(), ['message', 'read', '--all'], 10_000, implEnv());

    // Send to group
    const output = thrumIn(getTestRoot(), ['send', 'Group message to test-team', '--to', '@test-team'], 10_000, coordEnv());
    expect(output.toLowerCase()).toMatch(/sent|msg_/);

    // Implementer should receive it (is a member of test-team)
    const inbox = thrumIn(getImplementerRoot(), ['inbox', '--unread'], 10_000, implEnv());
    expect(inbox.toLowerCase()).toContain('group message');
  });

  test('F5: Nested groups not supported', async () => {
    thrum(['group', 'create', 'meta-group']);
    thrum(['group', 'add', 'meta-group', 'test-team']);

    const list = thrum(['group', 'list']);
    expect(list.toLowerCase()).toContain('meta-group');
  });
});

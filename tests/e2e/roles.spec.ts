/**
 * Role Template Tests — thrum-0sb
 *
 * Tests for role template application on agent registration:
 * - Creating a role template file
 * - Registering an agent with a matching role
 * - Verifying the preamble file is created with rendered template content
 * - Verifying `thrum roles list` shows the template
 * - Cleanup
 *
 * The role template feature runs entirely in the CLI (not daemon), so these
 * tests write template files directly and verify preamble files on disk.
 */
import { test, expect } from '@playwright/test';
import { thrumIn, getTestRoot } from './helpers/thrum-cli.js';
import * as fs from 'node:fs';
import * as path from 'node:path';

const AGENT_NAME = 'role_test_agent';
const AGENT_ROLE = 'tester_role_tmpl';

/** Dedicated env for role template tests — avoids session conflicts with other specs. */
function roleTestEnv(): NodeJS.ProcessEnv {
  return {
    ...process.env,
    THRUM_NAME: AGENT_NAME,
    THRUM_ROLE: AGENT_ROLE,
    THRUM_MODULE: 'all',
  };
}

test.describe('Role Templates', () => {
  test.describe.configure({ mode: 'serial' });

  let testRoot: string;
  let thrumDir: string;
  let templatePath: string;
  let preamblePath: string;

  test.beforeAll(async () => {
    testRoot = getTestRoot();
    thrumDir = path.join(testRoot, '.thrum');
    templatePath = path.join(thrumDir, 'role_templates', `${AGENT_ROLE}.md`);
    preamblePath = path.join(thrumDir, 'context', `${AGENT_NAME}_preamble.md`);

    // Ensure role_templates directory exists
    fs.mkdirSync(path.join(thrumDir, 'role_templates'), { recursive: true });

    // Clean up any leftover state from previous runs
    try { fs.rmSync(templatePath, { force: true }); } catch { /* ok */ }
    try { fs.rmSync(preamblePath, { force: true }); } catch { /* ok */ }
  });

  test.afterAll(async () => {
    // Clean up: remove template and preamble files created by this test suite
    try { fs.rmSync(templatePath, { force: true }); } catch { /* ok */ }
    try { fs.rmSync(preamblePath, { force: true }); } catch { /* ok */ }

    // Delete the agent identity file
    const identityPath = path.join(thrumDir, 'identities', `${AGENT_NAME}.json`);
    try { fs.rmSync(identityPath, { force: true }); } catch { /* ok */ }
  });

  test('RT-01: Create role template file', async () => {
    // Act: write a role template with Go text/template variables
    const templateContent = [
      `# Role: ${AGENT_ROLE}`,
      '',
      'Agent: {{.AgentName}}',
      'Role: {{.Role}}',
      'Module: {{.Module}}',
    ].join('\n');

    fs.writeFileSync(templatePath, templateContent, 'utf-8');

    // Assert: file exists with expected content
    expect(fs.existsSync(templatePath)).toBe(true);
    const written = fs.readFileSync(templatePath, 'utf-8');
    expect(written).toContain('{{.AgentName}}');
    expect(written).toContain('{{.Role}}');
  });

  test('RT-02: thrum roles list shows the template', async () => {
    // Act: list role templates
    const output = thrumIn(testRoot, ['roles', 'list'], 10_000, roleTestEnv());

    // Assert: the template file is listed
    expect(output).toContain(AGENT_ROLE);
  });

  test('RT-03: Register agent with matching role applies template as preamble', async () => {
    // Precondition: preamble should not exist yet
    expect(fs.existsSync(preamblePath)).toBe(false);

    // Act: register agent via quickstart with the template role
    const result = thrumIn(
      testRoot,
      [
        'quickstart',
        '--role', AGENT_ROLE,
        '--module', 'all',
        '--name', AGENT_NAME,
        '--intent', 'Role template E2E test',
      ],
      15_000,
      roleTestEnv(),
    );

    // Assert: quickstart completed (registered + session started)
    expect(result).toMatch(/registered|quickstart|session/i);
  });

  test('RT-04: Preamble file created at .thrum/context/{agent_name}_preamble.md', async () => {
    // Assert: preamble file exists after registration
    expect(fs.existsSync(preamblePath)).toBe(true);
  });

  test('RT-05: Preamble content contains rendered template variables', async () => {
    // Read the generated preamble
    const content = fs.readFileSync(preamblePath, 'utf-8');

    // Assert: template variables were substituted (not raw {{.X}} tokens)
    expect(content).not.toContain('{{.AgentName}}');
    expect(content).not.toContain('{{.Role}}');

    // Assert: rendered values are present
    expect(content).toContain(AGENT_NAME);
    expect(content).toContain(AGENT_ROLE);
  });

  test('RT-06: thrum roles list shows template with agent after registration', async () => {
    // Act: list role templates again — now the agent is registered
    const output = thrumIn(testRoot, ['roles', 'list'], 10_000, roleTestEnv());

    // Assert: template is listed and agent is associated with it
    expect(output).toContain(AGENT_ROLE);
    expect(output).toContain(AGENT_NAME);
  });
});

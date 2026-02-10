import { execFileSync } from 'node:child_process';
import path from 'node:path';

const ROOT = path.resolve(__dirname, '../../..');
const BIN = path.join(ROOT, 'bin', 'thrum');

/**
 * Default test agent identity used by the global-setup daemon.
 * These env vars serve as fallback when identity file resolution fails
 * (e.g., when multiple identity files exist in .thrum/identities/).
 */
export const TEST_AGENT_ROLE = 'tester';
export const TEST_AGENT_MODULE = 'e2e';

/**
 * Environment variables passed to all thrum CLI invocations.
 * THRUM_ROLE and THRUM_MODULE provide fallback identity resolution
 * so commands don't fail with "role not specified" when multiple
 * identity files exist.
 */
function thrumEnv(): NodeJS.ProcessEnv {
  return {
    ...process.env,
    THRUM_ROLE: TEST_AGENT_ROLE,
    THRUM_MODULE: TEST_AGENT_MODULE,
  };
}

/**
 * Execute a thrum CLI command safely (no shell injection).
 * Returns trimmed stdout.
 *
 * @param args - CLI arguments
 * @param timeout - command timeout in ms (default 10s)
 * @param env - optional environment overrides
 */
export function thrum(args: string[], timeout = 10_000, env?: NodeJS.ProcessEnv): string {
  try {
    return execFileSync(BIN, args, {
      cwd: ROOT,
      encoding: 'utf-8',
      timeout,
      env: env ?? thrumEnv(),
    }).trim();
  } catch (err: any) {
    const stderr = err.stderr?.toString() || '';
    throw new Error(
      `thrum ${args.join(' ')} failed (exit ${err.status}):\n${stderr}`,
    );
  }
}

/**
 * Execute a thrum CLI command with --json flag and parse the result.
 */
export function thrumJson<T>(args: string[], env?: NodeJS.ProcessEnv): T {
  const output = thrum([...args, '--json'], 10_000, env);
  try {
    return JSON.parse(output);
  } catch {
    throw new Error(`Invalid JSON from thrum ${args.join(' ')}:\n${output}`);
  }
}

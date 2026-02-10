import { execFileSync } from 'node:child_process';
import path from 'node:path';

const ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(ROOT, 'bin', 'thrum');

export default async function globalTeardown(): Promise<void> {
  console.log('[global-teardown] Stopping daemon...');
  try {
    execFileSync(BIN, ['daemon', 'stop'], {
      cwd: ROOT,
      encoding: 'utf-8',
      timeout: 15_000,
    });
    console.log('[global-teardown] Daemon stopped.');
  } catch (err: any) {
    const stderr = err.stderr?.toString() || err.message || '';
    if (stderr.toLowerCase().includes('not running') || stderr.toLowerCase().includes('no daemon')) {
      console.log('[global-teardown] Daemon was not running (already stopped).');
    } else {
      console.error('[global-teardown] Unexpected error stopping daemon:', stderr);
    }
  }
}

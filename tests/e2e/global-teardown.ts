import { existsSync, readFileSync, unlinkSync } from 'node:fs';
import path from 'node:path';
import { TEST_REPO_FILE, IMPLEMENTER_REPO_FILE, BARE_REMOTE_FILE, WS_PORT_FILE } from './global-setup.js';

const SOURCE_ROOT = path.resolve(__dirname, '../..');
const BIN = path.join(SOURCE_ROOT, 'bin', 'thrum');

export default async function globalTeardown(): Promise<void> {
  // Read paths for summary
  let coordinatorDir: string | undefined;
  let implementerDir: string | undefined;

  try {
    if (existsSync(TEST_REPO_FILE)) {
      coordinatorDir = readFileSync(TEST_REPO_FILE, 'utf-8').trim();
    }
  } catch { /* best effort */ }

  try {
    if (existsSync(IMPLEMENTER_REPO_FILE)) {
      implementerDir = readFileSync(IMPLEMENTER_REPO_FILE, 'utf-8').trim();
    }
  } catch { /* best effort */ }

  // Print summary — do NOT stop daemon or delete anything
  console.log('');
  console.log('[global-teardown] Test artifacts preserved:');
  if (coordinatorDir) {
    console.log(`[global-teardown]   coordinator/ — ${coordinatorDir}`);
  }
  if (implementerDir) {
    console.log(`[global-teardown]   implementer/ — ${implementerDir}`);
  }
  console.log('[global-teardown]');
  if (coordinatorDir) {
    console.log(`[global-teardown] Daemon still running — inspect with:`);
    console.log(`[global-teardown]   ${BIN} --repo ${coordinatorDir} daemon status`);
  }
  console.log(`[global-teardown] Run 'make clean-e2e' to stop daemon and remove everything.`);

  // Remove marker files only (tests are done, markers are stale)
  for (const file of [TEST_REPO_FILE, IMPLEMENTER_REPO_FILE, BARE_REMOTE_FILE, WS_PORT_FILE]) {
    try {
      if (existsSync(file)) unlinkSync(file);
    } catch { /* best effort */ }
  }
}

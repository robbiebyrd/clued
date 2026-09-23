import { execFile } from 'child_process';
import { promisify } from 'util';

const execFileAsync = promisify(execFile);

export async function getGitOrigin(cwd) {
  try {
    const { stdout } = await execFileAsync(
      'git', ['-C', cwd, 'remote', 'get-url', 'origin'],
      { timeout: 2000 }
    );
    return stdout.trim() || null;
  } catch {
    return null;
  }
}

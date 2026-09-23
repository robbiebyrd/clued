import { execFile } from 'child_process';
import { promisify } from 'util';

const execFileAsync = promisify(execFile);

export async function getGitOrigin(cwd: string): Promise<string | null> {
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

export async function getGitBranch(cwd: string): Promise<string | null> {
  try {
    const { stdout } = await execFileAsync(
      'git', ['-C', cwd, 'branch', '--show-current'],
      { timeout: 2000 }
    );
    return stdout.trim() || null;
  } catch {
    return null;
  }
}

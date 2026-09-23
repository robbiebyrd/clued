import { execFile } from 'child_process';

export function getGitOrigin(dir) {
  return new Promise(resolve => {
    execFile('git', ['-C', dir, 'remote', 'get-url', 'origin'], (err, stdout) => {
      resolve(err ? null : stdout.trim() || null);
    });
  });
}

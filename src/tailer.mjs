import fs from 'fs';
import readline from 'readline';

export function tailFile(filePath, onLine) {
  let pos = 0;
  let stopped = false;
  let watcher = null;
  let pollTimer = null;
  let inFlight = false;

  const read = () => {
    if (stopped || inFlight) return;
    inFlight = true;
    try {
      const size = fs.statSync(filePath).size;
      if (size <= pos) { inFlight = false; return; }
      const rl = readline.createInterface({
        input: fs.createReadStream(filePath, { start: pos, end: size - 1 }),
        crlfDelay: Infinity,
      });
      const batch = [];
      rl.on('line', l => { if (l.trim()) batch.push(l); });
      rl.on('error', () => { rl.close(); inFlight = false; });
      rl.on('close', () => { pos = size; inFlight = false; batch.forEach(onLine); });
    } catch { inFlight = false; /* file temporarily unavailable */ }
  };

  const start = () => {
    if (stopped) return;
    if (!fs.existsSync(filePath)) { setTimeout(start, 500); return; }
    read();
    try { watcher = fs.watch(filePath, read); } catch { /* fall through to poll-only */ }
    pollTimer = setInterval(read, 2000);
  };

  start();

  return {
    stop() {
      stopped = true;
      if (watcher)    { watcher.close();        watcher = null;    }
      if (pollTimer)  { clearInterval(pollTimer); pollTimer = null; }
    },
  };
}

import { hostname as osHostname, networkInterfaces } from 'os';

export interface HostInfo {
  hostname: string;
  ip:       string | null;
  mac:      string | null;
}

export function readHostInfo(): HostInfo {
  const hostname = osHostname();
  const ifaces   = networkInterfaces();

  for (const iface of Object.values(ifaces)) {
    if (!iface) continue;
    const entry = iface.find(a => a.family === 'IPv4' && !a.internal);
    if (entry) return { hostname, ip: entry.address, mac: entry.mac };
  }

  return { hostname, ip: null, mac: null };
}

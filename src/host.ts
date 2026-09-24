import { hostname as osHostname, networkInterfaces, userInfo, platform, arch, release, type as osType } from 'os';

export interface HostInfo {
  hostname:   string;
  ip:         string | null;
  mac:        string | null;
  username:   string;
  uid:        number;
  platform:   string;
  arch:       string;
  os_release: string;
  os_type:    string;
}

export function readHostInfo(): HostInfo {
  const hostname = osHostname();
  const ifaces   = networkInterfaces();
  const user     = userInfo();

  const base: HostInfo = {
    hostname,
    ip:         null,
    mac:        null,
    username:   user.username,
    uid:        user.uid,
    platform:   platform(),
    arch:       arch(),
    os_release: release(),
    os_type:    osType(),
  };

  for (const iface of Object.values(ifaces)) {
    if (!iface) continue;
    const entry = iface.find(a => a.family === 'IPv4' && !a.internal);
    if (entry) return { ...base, ip: entry.address, mac: entry.mac };
  }

  return base;
}

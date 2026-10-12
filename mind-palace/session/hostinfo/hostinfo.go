// Package hostinfo describes the machine the process runs on.
package hostinfo

import (
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Read collects host details. IP and MAC come from the first interface that is
// up, not loopback, has a hardware address and an IPv4 address; they stay nil
// when no interface qualifies.
func Read() session.HostInfo {
	hostname, _ := os.Hostname()
	h := session.HostInfo{
		Hostname:  hostname,
		UID:       -1,
		Platform:  runtime.GOOS,
		Arch:      runtime.GOARCH,
		OSRelease: uname("-r"),
		OSType:    uname("-s"),
	}
	if u, err := user.Current(); err == nil {
		h.Username = u.Username
		if uid, err := strconv.Atoi(u.Uid); err == nil {
			h.UID = uid
		}
	}
	h.IP, h.MAC = primaryInterface()
	return h
}

func primaryInterface() (ip, mac *string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			addr, hw := ipNet.IP.String(), iface.HardwareAddr.String()
			return &addr, &hw
		}
	}
	return nil, nil
}

// uname returns the output of uname with the given flag; the standard library
// has no portable kernel-release call (syscall.Uname is Linux-only).
func uname(flag string) string {
	out, err := exec.Command("uname", flag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

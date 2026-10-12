package hostinfo

import (
	"regexp"
	"testing"
)

var (
	ipv4Re = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
	macRe  = regexp.MustCompile(`(?i)^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
)

func TestReadStringFieldsNonEmpty(t *testing.T) {
	h := Read()
	fields := map[string]string{
		"hostname": h.Hostname, "username": h.Username, "platform": h.Platform,
		"arch": h.Arch, "os_release": h.OSRelease, "os_type": h.OSType,
	}
	for name, v := range fields {
		if v == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestReadIPLooksLikeIPv4WhenPresent(t *testing.T) {
	if h := Read(); h.IP != nil && !ipv4Re.MatchString(*h.IP) {
		t.Fatalf("ip %q is not IPv4", *h.IP)
	}
}

func TestReadMACLooksLikeMACWhenPresent(t *testing.T) {
	if h := Read(); h.MAC != nil && !macRe.MatchString(*h.MAC) {
		t.Fatalf("mac %q is not a MAC address", *h.MAC)
	}
}

func TestReadIPAndMACPresentTogether(t *testing.T) {
	if h := Read(); (h.IP == nil) != (h.MAC == nil) {
		t.Fatalf("ip=%v mac=%v: must be both set or both nil", h.IP, h.MAC)
	}
}

func TestReadUIDIsNumeric(t *testing.T) {
	if h := Read(); h.UID < 0 {
		t.Fatalf("uid = %d", h.UID)
	}
}

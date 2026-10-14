package stack

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TrustStep is one trust store the CA goes into (or comes out of): the
// commands that do it, and whether they need root.
type TrustStep struct {
	Store    string     `json:"store"`
	Commands [][]string `json:"commands"`
	Root     bool       `json:"needs_root"`
	// Done is set once the commands ran; Error says why they did not.
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

// Line is the step as one shell line, for a person to run.
func (s TrustStep) Line() string {
	var parts []string
	for _, c := range s.Commands {
		words := make([]string, len(c))
		for i, w := range c {
			words[i] = shellQuote(w)
		}
		line := strings.Join(words, " ")
		if s.Root {
			line = "sudo " + line
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " && ")
}

func shellQuote(w string) string {
	if w != "" && !strings.ContainsAny(w, " \t\n'\"\\$`;&|<>()*?[]#~!{}") {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// TrustEnv is what the plan depends on; DefaultTrustEnv reads this machine.
type TrustEnv struct {
	GOOS     string
	Home     string
	LookPath func(string) (string, error)
	Exists   func(string) bool
	Glob     func(string) ([]string, error)
}

// DefaultTrustEnv is this machine's environment.
func DefaultTrustEnv() TrustEnv {
	home, _ := os.UserHomeDir()
	return TrustEnv{
		GOOS:     runtime.GOOS,
		Home:     home,
		LookPath: exec.LookPath,
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		Glob:     filepath.Glob,
	}
}

func (e TrustEnv) has(cmd string) bool { _, err := e.LookPath(cmd); return err == nil }

// TrustPlan lists the trust stores of this machine that the CA at caPath
// goes into, or comes out of with remove:
//
//   - macOS: the login keychain, as a trusted root (no root needed; macOS
//     may ask for the account password). Safari, Chrome, curl, Go and
//     Firefox (which reads the system roots on macOS) then trust it.
//   - Linux: the system store, through update-ca-certificates (Debian,
//     Ubuntu, Alpine, openSUSE), update-ca-trust (Fedora, RHEL) or p11-kit's
//     trust (Arch); this needs root. Then the NSS databases Chrome and
//     Chromium (~/.pki/nssdb) and each Firefox profile read, with certutil.
//   - Windows: the current user's Root store, with certutil (Windows asks
//     to confirm).
//
// Notes are the stores it cannot reach (certutil missing, no system tool).
func TrustPlan(env TrustEnv, caPath string, cert *x509.Certificate, remove bool) (steps []TrustStep, notes []string) {
	nick := "clued server CA " + Fingerprint(cert)[:12]
	switch env.GOOS {
	case "darwin":
		keychain := filepath.Join(env.Home, "Library", "Keychains", "login.keychain-db")
		if remove {
			steps = append(steps, TrustStep{Store: "macOS login keychain", Commands: [][]string{
				{"security", "remove-trusted-cert", caPath},
				{"security", "delete-certificate", "-Z", sha1Fingerprint(cert), keychain},
			}})
		} else {
			steps = append(steps, TrustStep{Store: "macOS login keychain", Commands: [][]string{
				{"security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, caPath},
			}})
		}
	case "windows":
		if remove {
			steps = append(steps, TrustStep{Store: "Windows user Root store", Commands: [][]string{
				{"certutil", "-user", "-delstore", "Root", cert.SerialNumber.Text(16)},
			}})
		} else {
			steps = append(steps, TrustStep{Store: "Windows user Root store", Commands: [][]string{
				{"certutil", "-user", "-addstore", "Root", caPath},
			}})
		}
	case "linux":
		file := "clued-server-ca-" + Fingerprint(cert)[:12] + ".crt"
		system := func(store, dir, update string) TrustStep {
			target := filepath.Join(dir, file)
			if remove {
				return TrustStep{Store: store, Root: true, Commands: [][]string{{"rm", "-f", target}, {update}}}
			}
			return TrustStep{Store: store, Root: true, Commands: [][]string{{"install", "-m", "0644", caPath, target}, {update}}}
		}
		switch {
		case env.Exists("/usr/local/share/ca-certificates") && env.has("update-ca-certificates"):
			steps = append(steps, system("system store (update-ca-certificates)", "/usr/local/share/ca-certificates", "update-ca-certificates"))
		case env.Exists("/etc/pki/ca-trust/source/anchors") && env.has("update-ca-trust"):
			steps = append(steps, system("system store (update-ca-trust)", "/etc/pki/ca-trust/source/anchors", "update-ca-trust"))
		case env.Exists("/etc/pki/trust/anchors") && env.has("update-ca-certificates"):
			steps = append(steps, system("system store (update-ca-certificates)", "/etc/pki/trust/anchors", "update-ca-certificates"))
		case env.has("trust"):
			if remove {
				steps = append(steps, TrustStep{Store: "system store (p11-kit trust)", Root: true, Commands: [][]string{{"trust", "anchor", "--remove", caPath}}})
			} else {
				steps = append(steps, TrustStep{Store: "system store (p11-kit trust)", Root: true, Commands: [][]string{{"trust", "anchor", "--store", caPath}}})
			}
		default:
			notes = append(notes, "no system trust tool found (update-ca-certificates, update-ca-trust or trust); add "+caPath+" to the system store by hand")
		}
		var dbs []string
		if chrome := filepath.Join(env.Home, ".pki", "nssdb"); env.Exists(chrome) {
			dbs = append(dbs, chrome)
		}
		for _, pattern := range []string{
			filepath.Join(env.Home, ".mozilla", "firefox", "*", "cert9.db"),
			filepath.Join(env.Home, "snap", "firefox", "common", ".mozilla", "firefox", "*", "cert9.db"),
		} {
			found, _ := env.Glob(pattern)
			for _, db := range found {
				dbs = append(dbs, filepath.Dir(db))
			}
		}
		if len(dbs) > 0 && !env.has("certutil") {
			notes = append(notes, "Chrome or Firefox keep their own certificate databases on Linux; install certutil (libnss3-tools on Debian and Ubuntu, nss-tools on Fedora) and run server trust again to trust the CA there too")
		} else {
			for _, db := range dbs {
				store := "NSS database " + db
				if remove {
					steps = append(steps, TrustStep{Store: store, Commands: [][]string{{"certutil", "-d", "sql:" + db, "-D", "-n", nick}}})
				} else {
					steps = append(steps, TrustStep{Store: store, Commands: [][]string{{"certutil", "-d", "sql:" + db, "-A", "-t", "C,,", "-n", nick, "-i", caPath}}})
				}
			}
		}
	default:
		notes = append(notes, "no known trust store on "+env.GOOS+"; add "+caPath+" to the system's trusted roots by hand")
	}
	return steps, notes
}

// Runner runs one command, returning its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs commands on this machine.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ApplyTrust runs the steps. A step that needs root runs as is when
// asRoot, through `sudo -n` when sudo works without a password, and is
// left for the person to run otherwise (Pending lists its shell line). A
// failing command stops its step and records the output.
func ApplyTrust(ctx context.Context, steps []TrustStep, run Runner, asRoot bool) (done []TrustStep, pending []string) {
	sudo := !asRoot && func() bool { _, err := run(ctx, "sudo", "-n", "true"); return err == nil }()
	for _, s := range steps {
		if s.Root && !asRoot && !sudo {
			pending = append(pending, s.Line())
			done = append(done, s)
			continue
		}
		s.Done = true
		for _, c := range s.Commands {
			name, args := c[0], c[1:]
			if s.Root && !asRoot {
				name, args = "sudo", append([]string{"-n", c[0]}, c[1:]...)
			}
			if out, err := run(ctx, name, args...); err != nil {
				s.Done = false
				s.Error = strings.TrimSpace(err.Error() + ": " + lastLines(string(out), 3))
				break
			}
		}
		if !s.Done && !s.Root {
			// macOS refuses a keychain change it cannot ask the password
			// for (no window to ask in); the person can run it.
			pending = append(pending, s.Line())
		}
		done = append(done, s)
	}
	return done, pending
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// VerifyServer reports whether the server at url presents a certificate
// this machine's trust store accepts, without the stack's CA file: what a
// browser, curl or another client sees.
func VerifyServer(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("not an https:// URL")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/health", nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s/health answered %d", url, res.StatusCode)
	}
	return nil
}

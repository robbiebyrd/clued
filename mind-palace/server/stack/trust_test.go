package stack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func fakeEnv(goos string, commands []string, paths []string, globs map[string][]string) TrustEnv {
	has := map[string]bool{}
	for _, c := range commands {
		has[c] = true
	}
	exists := map[string]bool{}
	for _, p := range paths {
		exists[p] = true
	}
	return TrustEnv{
		GOOS: goos, Home: "/home/me",
		LookPath: func(c string) (string, error) {
			if has[c] {
				return "/usr/bin/" + c, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(p string) bool { return exists[p] },
		Glob:   func(p string) ([]string, error) { return globs[p], nil },
	}
}

func testCA(t *testing.T) (string, *Result) {
	t.Helper()
	opts := testOptions(t)
	res, err := Init(opts)
	if err != nil {
		t.Fatal(err)
	}
	return res.CAFile, &res
}

func lines(steps []TrustStep) string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Store+": "+s.Line())
	}
	return strings.Join(out, "\n")
}

func TestTrustPlanPerPlatform(t *testing.T) {
	caPath, _ := testCA(t)
	cert, err := ReadCA(caPath)
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(cert)[:12]
	firefox := "/home/me/.mozilla/firefox/abc.default-release"
	cases := []struct {
		name    string
		env     TrustEnv
		remove  bool
		want    []string
		root    int
		noteHas string
	}{
		{name: "macOS", env: fakeEnv("darwin", nil, nil, nil),
			want: []string{"macOS login keychain: security add-trusted-cert -r trustRoot -k /home/me/Library/Keychains/login.keychain-db " + caPath}},
		{name: "macOS remove", env: fakeEnv("darwin", nil, nil, nil), remove: true,
			want: []string{"security remove-trusted-cert " + caPath, "security delete-certificate -Z " + sha1Fingerprint(cert)}},
		{name: "Debian with Chrome and Firefox", root: 1,
			env: fakeEnv("linux", []string{"update-ca-certificates", "certutil"}, []string{"/usr/local/share/ca-certificates", "/home/me/.pki/nssdb"},
				map[string][]string{"/home/me/.mozilla/firefox/*/cert9.db": {firefox + "/cert9.db"}}),
			want: []string{
				"sudo install -m 0644 " + caPath + " /usr/local/share/ca-certificates/clued-server-ca-" + fp + ".crt && sudo update-ca-certificates",
				"NSS database /home/me/.pki/nssdb: certutil -d sql:/home/me/.pki/nssdb -A -t C,, -n 'clued server CA " + fp + "' -i " + caPath,
				"certutil -d sql:" + firefox + " -A",
			}},
		{name: "Fedora", root: 1, env: fakeEnv("linux", []string{"update-ca-trust"}, []string{"/etc/pki/ca-trust/source/anchors"}, nil),
			want: []string{"sudo install -m 0644 " + caPath + " /etc/pki/ca-trust/source/anchors/clued-server-ca-" + fp + ".crt && sudo update-ca-trust"}},
		{name: "Arch", root: 1, env: fakeEnv("linux", []string{"trust"}, nil, nil),
			want: []string{"sudo trust anchor --store " + caPath}},
		{name: "Debian remove", root: 1, remove: true, env: fakeEnv("linux", []string{"update-ca-certificates"}, []string{"/usr/local/share/ca-certificates"}, nil),
			want: []string{"sudo rm -f /usr/local/share/ca-certificates/clued-server-ca-" + fp + ".crt && sudo update-ca-certificates"}},
		{name: "Linux without certutil", root: 1, noteHas: "install certutil",
			env:  fakeEnv("linux", []string{"update-ca-certificates"}, []string{"/usr/local/share/ca-certificates", "/home/me/.pki/nssdb"}, nil),
			want: []string{"update-ca-certificates"}},
		{name: "Linux without a system tool", noteHas: "no system trust tool", env: fakeEnv("linux", nil, nil, nil)},
		{name: "Windows", env: fakeEnv("windows", nil, nil, nil), want: []string{"certutil -user -addstore Root " + caPath}},
		{name: "Windows remove", remove: true, env: fakeEnv("windows", nil, nil, nil), want: []string{"certutil -user -delstore Root " + cert.SerialNumber.Text(16)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps, notes := TrustPlan(c.env, caPath, cert, c.remove)
			all := lines(steps)
			for _, w := range c.want {
				if !strings.Contains(all, w) {
					t.Errorf("plan lacks %q:\n%s", w, all)
				}
			}
			root := 0
			for _, s := range steps {
				if s.Root {
					root++
				}
			}
			if root != c.root {
				t.Errorf("%d steps need root, want %d:\n%s", root, c.root, all)
			}
			if c.noteHas != "" && !strings.Contains(strings.Join(notes, " "), c.noteHas) {
				t.Errorf("notes = %v, want %q", notes, c.noteHas)
			}
		})
	}
}

// recorder is a Runner that records commands and fails the ones named.
type recorder struct {
	ran  []string
	fail map[string]bool
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	r.ran = append(r.ran, line)
	for prefix := range r.fail {
		if strings.HasPrefix(line, prefix) {
			return []byte("nope"), fmt.Errorf("exit 1")
		}
	}
	return nil, nil
}

func TestApplyTrustRunsWhatItCanAndListsTheRest(t *testing.T) {
	caPath, _ := testCA(t)
	cert, _ := ReadCA(caPath)
	env := fakeEnv("linux", []string{"update-ca-certificates", "certutil"}, []string{"/usr/local/share/ca-certificates", "/home/me/.pki/nssdb"}, nil)
	steps, _ := TrustPlan(env, caPath, cert, false)

	// No root and no passwordless sudo: the user-level NSS step runs, the
	// system step is left as a sudo line.
	r := &recorder{fail: map[string]bool{"sudo -n true": true}}
	done, pending := ApplyTrust(context.Background(), steps, r.run, false)
	if len(pending) != 1 || !strings.HasPrefix(pending[0], "sudo install -m 0644 ") {
		t.Fatalf("pending = %v", pending)
	}
	if !done[1].Done || done[0].Done {
		t.Errorf("done = %+v", done)
	}
	if strings.Contains(strings.Join(r.ran, "\n"), "update-ca-certificates") {
		t.Errorf("ran a root command without root: %v", r.ran)
	}

	// Passwordless sudo: the system step runs through sudo -n.
	r = &recorder{}
	_, pending = ApplyTrust(context.Background(), steps, r.run, false)
	if len(pending) != 0 || !strings.Contains(strings.Join(r.ran, "\n"), "sudo -n update-ca-certificates") {
		t.Errorf("with sudo: pending %v, ran %v", pending, r.ran)
	}

	// As root: run directly.
	r = &recorder{}
	ApplyTrust(context.Background(), steps, r.run, true)
	if all := strings.Join(r.ran, "\n"); !strings.Contains(all, "update-ca-certificates") || strings.Contains(all, "sudo") {
		t.Errorf("as root ran %v", r.ran)
	}

	// A user-level step that fails (macOS cannot ask for the password) is
	// left for the person, with the reason.
	mac, _ := TrustPlan(fakeEnv("darwin", nil, nil, nil), caPath, cert, false)
	r = &recorder{fail: map[string]bool{"security": true}}
	done, pending = ApplyTrust(context.Background(), mac, r.run, false)
	if len(pending) != 1 || done[0].Done || !strings.Contains(done[0].Error, "nope") {
		t.Errorf("failed keychain step: pending %v, %+v", pending, done[0])
	}
}

func TestVerifyServerNeedsTheSystemToTrustIt(t *testing.T) {
	if err := VerifyServer(context.Background(), "http://localhost:1"); err == nil {
		t.Error("an http URL verified")
	}
}

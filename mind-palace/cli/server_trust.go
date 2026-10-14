package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/server/stack"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// trustRunner and trustEnv run and describe the trust stores; tests
// replace them.
var (
	trustRunner = stack.ExecRunner
	trustEnv    = stack.DefaultTrustEnv
	trustAsRoot = func() bool { return os.Geteuid() == 0 }
)

// trustResult is what `server trust` prints.
type trustResult struct {
	CA          string            `json:"ca"`
	Subject     string            `json:"subject"`
	Fingerprint string            `json:"fingerprint"`
	Removed     bool              `json:"removed,omitempty"`
	Steps       []stack.TrustStep `json:"steps"`
	// Pending are the shell lines left for the person to run (root, or a
	// prompt this process could not show).
	Pending []string `json:"pending"`
	Notes   []string `json:"notes,omitempty"`
	// Verified says whether the server at URL now verifies with this
	// machine's trust store alone; VerifyError says why not.
	URL         string `json:"url,omitempty"`
	Verified    *bool  `json:"verified,omitempty"`
	VerifyError string `json:"verify_error,omitempty"`
}

// serverTrustCmd is `server trust`: puts the stack's CA in this machine's
// trust stores, or takes it out.
func (a *App) serverTrustCmd() *cobra.Command {
	var dir, caPath, url string
	var remove, dryRun bool
	cmd := &cobra.Command{
		Use:   "trust [--dir ~/.clued-server] [--ca file] [--url https://localhost:8443] [--remove] [--dry-run]",
		Short: "Trust the server's CA on this machine (keychain, system store, Chrome and Firefox), or remove it",
		Long: `Installs the CA that server init generated (<dir>/tls/ca.pem, or --ca) in
this machine's trust stores, so browsers, curl and other tools accept the
server's certificate without serverCaFile:

  macOS    the login keychain, as a trusted root (macOS may ask for the
           account password)
  Linux    the system store (update-ca-certificates, update-ca-trust or
           p11-kit trust; needs root), then the NSS databases of Chrome and
           Firefox with certutil when they exist
  Windows  the current user's Root store (Windows asks to confirm)

Steps that need root run when this runs as root or sudo works without a
password; otherwise they are listed under "pending" as the exact commands
to run. --url (default the stack's https://localhost:<port>) is then
checked with the system roots alone. --remove takes the CA out of the same
stores; --dry-run only lists the steps. The CA is name-constrained to the
stack's hosts, so trusting it vouches for this server and nothing else.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
				}
				dir = filepath.Join(home, ".clued-server")
			}
			if caPath == "" {
				caPath = filepath.Join(dir, stack.CAFile)
			}
			caPath, _ = filepath.Abs(caPath)
			cert, err := stack.ReadCA(caPath)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindNotFound, Message: err.Error() + " (run server init first, or pass --ca)"})
			}
			steps, notes := stack.TrustPlan(trustEnv(), caPath, cert, remove)
			res := trustResult{CA: caPath, Subject: cert.Subject.CommonName, Fingerprint: stack.Fingerprint(cert), Removed: remove, Steps: steps, Pending: []string{}, Notes: notes}
			if dryRun {
				for _, s := range steps {
					res.Pending = append(res.Pending, s.Line())
				}
				a.print(a.Stdout, map[string]any{"ok": true, "result": res})
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			res.Steps, res.Pending = stack.ApplyTrust(ctx, steps, trustRunner, trustAsRoot())
			if res.Pending == nil {
				res.Pending = []string{}
			}
			if url == "" && !remove {
				url = stackURL(dir)
			}
			if url != "" && !remove && len(res.Pending) == 0 {
				res.URL = url
				err := stack.VerifyServer(ctx, url)
				ok := err == nil
				res.Verified = &ok
				if err != nil {
					res.VerifyError = err.Error()
				}
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": res})
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", "", "stack directory (default: ~/.clued-server)")
	f.StringVar(&caPath, "ca", "", "CA certificate to trust (default: <dir>/tls/ca.pem)")
	f.StringVar(&url, "url", "", "server to check afterwards (default: the stack's https://localhost:<port>)")
	f.BoolVar(&remove, "remove", false, "take the CA out of the trust stores")
	f.BoolVar(&dryRun, "dry-run", false, "list the steps without running them")
	return cmd
}

// stackURL is the stack's local HTTPS URL from its compose file, or "" when
// it publishes plain HTTP or has none.
func stackURL(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, stack.ComposeFile))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		// - "${CLUED_PUBLIC_PORT:-8443}:443"
		if strings.HasSuffix(line, `}:443"`) {
			if i := strings.Index(line, ":-"); i >= 0 {
				if j := strings.Index(line[i:], "}"); j > 2 {
					return "https://localhost:" + line[i+2:i+j]
				}
			}
		}
	}
	return ""
}

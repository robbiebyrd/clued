package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Routes of the clued cloud the client commands use; a plain clued server
// answers 404 on them, which the commands treat as "not a cloud".
const (
	deviceCodePath   = "/api/v1/device/code"
	deviceTokenPath  = "/api/v1/device/token"
	meConfigPath     = "/api/v1/me/config"
	meCurrentToken   = "/api/v1/me/tokens/current"
	minPollInterval  = time.Second
	defaultLoginWait = 15 * time.Minute
)

// Config keys the server's client settings may set (the same names as in
// config.json); nothing else from the server reaches the file. The
// redaction keys carry a workspace's enforced capture redaction.
var pulledConfigKeys = []string{"enabledEnrichers", "disabledEnrichers", "projectsDir", "fileHistoryDir", "redactAtCapture", "redactPatterns", "excludePaths", captureContentKey}

// meConfig is the body of GET /api/v1/me/config. Managed lists the keys a
// workspace policy enforces and ManagedBy who manages each; the cloud may
// send them inside client_config (as config.json-style keys) or beside it.
type meConfig struct {
	ClientConfig map[string]any    `json:"client_config"`
	UpdatedAt    string            `json:"updated_at"`
	Managed      []string          `json:"managed"`
	ManagedBy    map[string]string `json:"managedBy"`
}

// managed returns the managed keys and their managers from wherever the
// body carries them.
func (m meConfig) managed() ([]string, map[string]string) {
	keys, by := m.Managed, m.ManagedBy
	if raw, ok := m.ClientConfig["managed"].([]any); ok && keys == nil {
		for _, k := range raw {
			if s, ok := k.(string); ok {
				keys = append(keys, s)
			}
		}
	}
	if raw, ok := m.ClientConfig["managedBy"].(map[string]any); ok && by == nil {
		by = map[string]string{}
		for k, v := range raw {
			if s, ok := v.(string); ok {
				by[k] = s
			}
		}
	}
	return keys, by
}

// readConfigJSON reads config.json as a document ({} when it is missing).
func readConfigJSON(path string) map[string]any {
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}
	return doc
}

// captureContentKey is the server's capture hint: the server alone sets it
// in remote mode, so config-pull removes it when the server sends none (a
// workspace back on full capture) and logout removes it with the server
// keys.
const captureContentKey = "captureContent"

// deviceStart is the body of POST /api/v1/device/code.
type deviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceLink is the body of POST /api/v1/device/token once approved.
type deviceLink struct {
	Token     string `json:"token"`
	TokenID   string `json:"token_id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
}

// sessionLoginCmd is `session login`: link this machine to a clued cloud
// through the device flow and write the token into config.json.
func (a *App) sessionLoginCmd() *cobra.Command {
	var (
		serverURL, name, caFile string
		insecure                bool
		wait                    time.Duration
	)
	c := &cobra.Command{
		Use:   "login --server URL [--name NAME] [--ca-file PEM] [--wait 15m]",
		Short: "Link this machine to a clued server: show a code, have it approved, and the token lands in config.json",
		Long: `Starts the device flow on the server at --server (POST /api/v1/device/code),
prints the code to approve on stderr, polls until it is approved, then
writes serverUrl and serverToken (and serverCaFile or serverInsecure when
given) into the config file, keeping every other key, with mode 0600, and
checks the token with /api/v1/whoami. On a self-hosted clued server the
code is approved from a machine already linked to it ("server device
approve <code>"); the clued cloud approves it in a browser at the printed
link. A denied or expired request fails the command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(serverURL) == "" {
				return a.sessionBadRequest("--server is required (the server's URL, e.g. https://clued.example.com:8443)")
			}
			cfg := session.Config{ServerURL: serverURL, ServerCAFile: caFile, ServerInsecure: insecure}
			client, err := remote.New(cfg)
			if err != nil {
				return a.sessionFail(err)
			}
			if name == "" {
				name = clientName()
			}
			ctx := cmd.Context()
			var start deviceStart
			err = client.Do(ctx, http.MethodPost, deviceCodePath, map[string]any{"client_name": name, "client_info": clientInfo()}, &start)
			if err != nil {
				// An older server has no device routes: the path is unknown
				// (404) or, since it sits behind the bearer check there,
				// refused for want of a token (401).
				if remote.IsNotFound(err) || errors.Is(err, remote.ErrUnauthorized) {
					return a.sessionBadRequest(fmt.Sprintf("%s offers no device login (no %s; an older clued server); put its token in %s by hand", client.BaseURL(), deviceCodePath, a.sess.configPath))
				}
				return a.sessionFail(err)
			}
			fmt.Fprintf(a.Stderr, "clued: approve the code %s from a machine linked to %s (`server device approve %s`), or open %s (expires in %d minutes)\n",
				start.UserCode, client.BaseURL(), start.UserCode, start.VerificationURIComplete, max(start.ExpiresIn/60, 1))

			link, err := pollDeviceLink(ctx, client, start, wait)
			if err != nil {
				return a.sessionFail(err)
			}
			set := map[string]any{"serverUrl": client.BaseURL(), "serverToken": link.Token}
			remove := []string{"serverCaFile", "serverInsecure"}
			if caFile != "" {
				set["serverCaFile"] = caFile
				remove = remove[1:]
			}
			if insecure {
				set["serverInsecure"] = true
				remove = remove[:1]
			}
			if err := session.UpdateConfigFile(a.sess.configPath, set, remove); err != nil {
				return a.sessionFail(err)
			}
			cfg.ServerToken = link.Token
			linked, err := remote.New(cfg)
			if err != nil {
				return a.sessionFail(err)
			}
			p, err := prime.Whoami(ctx, linked)
			if err != nil {
				return a.sessionFail(fmt.Errorf("the token was written to %s but whoami failed: %w", a.sess.configPath, err))
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": map[string]any{
				"server": client.BaseURL(), "name": p.Name, "account_id": p.AccountID, "token_id": link.TokenID, "version": p.Version, "config": a.sess.configPath,
			}})
			return nil
		},
	}
	c.Flags().StringVar(&serverURL, "server", os.Getenv("CLUED_SERVER_URL"), "the clued cloud's URL (CLUED_SERVER_URL)")
	c.Flags().StringVar(&name, "name", "", "how this client is listed in the cloud (default: the host name)")
	c.Flags().StringVar(&caFile, "ca-file", "", "PEM bundle of a private CA to trust, written as serverCaFile")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow a plain http:// server off loopback (written as serverInsecure)")
	c.Flags().DurationVar(&wait, "wait", defaultLoginWait, "how long to wait for the approval")
	return c
}

// pollDeviceLink polls the token endpoint at the server's interval until
// the token arrives or a final answer (denied, expired, unknown) comes.
func pollDeviceLink(ctx context.Context, client *remote.Client, start deviceStart, wait time.Duration) (deviceLink, error) {
	interval := max(time.Duration(start.Interval)*time.Second, minPollInterval)
	deadline := time.Now().Add(wait)
	for {
		select {
		case <-ctx.Done():
			return deviceLink{}, ctx.Err()
		case <-time.After(interval):
		}
		var link deviceLink
		err := client.Do(ctx, http.MethodPost, deviceTokenPath, map[string]string{"device_code": start.DeviceCode}, &link)
		if err == nil {
			if link.Token == "" {
				return deviceLink{}, errors.New("the cloud delivered an empty token")
			}
			return link, nil
		}
		var re *remote.Error
		if errors.As(err, &re) {
			switch re.Kind {
			case "authorization_pending":
			case "slow_down":
				interval += 5 * time.Second
			case "access_denied":
				return deviceLink{}, errors.New("the link was denied in the browser")
			case "expired_token":
				return deviceLink{}, errors.New("the code expired before it was approved; run login again")
			default:
				return deviceLink{}, err
			}
		} else {
			return deviceLink{}, err
		}
		if time.Now().After(deadline) {
			return deviceLink{}, fmt.Errorf("no approval within %s; run login again", wait)
		}
	}
}

// clientName is the default name a client links under: the short host name.
func clientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "clued client"
	}
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	return host
}

// clientInfo is what the cloud shows next to the code so the user can tell
// the request is theirs.
func clientInfo() map[string]string {
	info := map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}
	if host, err := os.Hostname(); err == nil {
		info["host"] = host
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		info["user"] = u.Username
	}
	return info
}

// sessionLogoutCmd is `session logout`: revoke this client's cloud token
// and take the server keys out of config.json.
func (a *App) sessionLogoutCmd() *cobra.Command {
	var keep bool
	c := &cobra.Command{
		Use:   "logout [--keep-config]",
		Short: "Revoke this client's token on the cloud and leave remote mode",
		Long: `Calls DELETE /api/v1/me/tokens/current on the configured server (a plain
clued server has no such route and is skipped; a token the server no longer
knows is fine too) and removes serverUrl, serverToken, serverCaFile,
serverInsecure and the server's captureContent hint from the config file, which puts the plugin back in local
mode. --keep-config revokes only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.sessionConfig()
			client, err := remote.FromConfigE(cfg)
			if err != nil {
				return a.sessionFail(err)
			}
			if client == nil {
				return a.sessionBadRequest(fmt.Sprintf("no server configured in %s (or CLUED_SERVER_URL)", a.sess.configPath))
			}
			revoked := false
			err = client.Do(cmd.Context(), http.MethodDelete, meCurrentToken, nil, nil)
			switch {
			case err == nil:
				revoked = true
			case remote.IsNotFound(err), errors.Is(err, remote.ErrUnauthorized):
			default:
				return a.sessionFail(err)
			}
			if !keep {
				if err := session.UpdateConfigFile(a.sess.configPath, nil, []string{"serverUrl", "serverToken", "serverCaFile", "serverInsecure", captureContentKey}); err != nil {
					return a.sessionFail(err)
				}
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": map[string]any{"server": client.BaseURL(), "revoked": revoked, "config": a.sess.configPath, "config_kept": keep}})
			return nil
		},
	}
	c.Flags().BoolVar(&keep, "keep-config", false, "revoke the token but leave the config file alone")
	return c
}

// sessionConfigPullCmd is `session config-pull`: apply the account's client
// settings from the cloud to config.json.
func (a *App) sessionConfigPullCmd() *cobra.Command {
	var quiet bool
	c := &cobra.Command{
		Use:   "config-pull [--quiet]",
		Short: "Apply the client settings managed in the clued cloud to config.json",
		Long: `Fetches GET /api/v1/me/config from the configured server and writes the
keys it carries (enabledEnrichers, disabledEnrichers, projectsDir,
fileHistoryDir, redactAtCapture, redactPatterns, excludePaths, captureContent)
into the config file, keeping every other key. Keys the server does not set
are left as they are, except captureContent, the server's capture hint,
which is removed when the server sends no boolean for it. Keys the server
lists as managed (a workspace policy enforces them) replace a local value
that differs, with a note on stderr naming who manages the key (printed
with --quiet too). Not in remote mode, or against a
plain clued server (no such route), nothing changes and the command
succeeds; the session-start hook runs it with --quiet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.sessionConfig()
			client, err := remote.FromConfigE(cfg)
			if err != nil {
				return a.sessionFail(err)
			}
			res := map[string]any{"remote": client != nil, "supported": false, "updated": []string{}}
			if client != nil {
				var body meConfig
				err := client.Do(cmd.Context(), http.MethodGet, meConfigPath, nil, &body)
				switch {
				case err == nil:
					set := map[string]any{}
					updated := []string{}
					for _, k := range pulledConfigKeys {
						if v, ok := body.ClientConfig[k]; ok && v != nil {
							if _, isBool := v.(bool); k == captureContentKey && !isBool {
								continue
							}
							set[k] = v
							updated = append(updated, k)
						}
					}
					managed, managedBy := body.managed()
					overridden := []string{}
					local := readConfigJSON(a.sess.configPath)
					for _, k := range managed {
						v, ok := set[k]
						if !ok {
							continue
						}
						if cur, has := local[k]; has && !reflect.DeepEqual(normalizeJSON(cur), normalizeJSON(v)) {
							overridden = append(overridden, k)
							by := managedBy[k]
							if by == "" {
								by = "your workspace"
							}
							fmt.Fprintf(a.Stderr, "clued: %s is managed by %s; the local value in %s was replaced\n", k, by, a.sess.configPath)
						}
					}
					remove := []string{}
					if _, ok := set[captureContentKey]; !ok && a.sessionConfig().CaptureContent != nil {
						remove = []string{captureContentKey}
					}
					if len(set) > 0 || len(remove) > 0 {
						if err := session.UpdateConfigFile(a.sess.configPath, set, remove); err != nil {
							return a.sessionFail(err)
						}
					}
					if managed == nil {
						managed = []string{}
					}
					res["supported"], res["updated"], res["removed"], res["updated_at"] = true, updated, remove, body.UpdatedAt
					res["managed"], res["overridden"] = managed, overridden
				case remote.IsNotFound(err):
				default:
					return a.sessionFail(err)
				}
			}
			if !quiet {
				a.print(a.Stdout, map[string]any{"ok": true, "result": res})
			}
			return nil
		},
	}
	c.Flags().BoolVar(&quiet, "quiet", false, "print nothing on success")
	return c
}

// normalizeJSON round-trips v through JSON so values read from a file and
// decoded from a response compare by content.
func normalizeJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

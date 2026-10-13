package cli

import (
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/device"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// serverDeviceCmd is `server device list|approve|deny`: the approver's side
// of `session login`, run over the server's API by a machine that already
// holds a token. The server and token come from the clued config (remote
// mode) unless --server and --token say otherwise.
func (a *App) serverDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Approve or deny machines logging in with `session login`",
		Long: `A machine without a token runs "session login --server URL" and shows a
code. On a machine that already holds a token for the server, "server device
approve <code>" mints the token for it, under this machine's account and
with this machine's projects unless --project narrows them; "deny" refuses
the request and "list" shows what is waiting. The server and the token to
act with come from the clued config in remote mode; --server and --token
override them.`,
	}
	pf := cmd.PersistentFlags()
	pf.String("server", "", "the clued server's URL (default: serverUrl from the clued config)")
	pf.String("token", "", "bearer token to act with (default: serverToken from the clued config)")
	pf.String("ca-file", "", "PEM bundle of a private CA to trust (default: serverCaFile from the clued config)")
	pf.Bool("insecure", false, "allow a plain http:// server off loopback")
	cmd.AddCommand(a.serverDeviceListCmd(), a.serverDeviceApproveCmd(), a.serverDeviceDenyCmd())
	return cmd
}

func (a *App) serverDeviceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the login requests waiting on the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.deviceClient(cmd)
			if err != nil {
				return err
			}
			var pending []device.Request
			if err := client.Do(cmd.Context(), http.MethodGet, server.PathDevicePending, nil, &pending); err != nil {
				return a.deviceFail(err)
			}
			if pending == nil {
				pending = []device.Request{}
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": pending})
			return nil
		},
	}
}

func (a *App) serverDeviceApproveCmd() *cobra.Command {
	var name string
	var projects []string
	cmd := &cobra.Command{
		Use:   "approve <code> [--name <token name>] [--project <slug>]...",
		Short: "Approve a login request: the server mints its token under this machine's account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.deviceClient(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{"user_code": args[0], "name": name, "projects": projects}
			var approval device.Approval
			if err := client.Do(cmd.Context(), http.MethodPost, server.PathDeviceApprove, body, &approval); err != nil {
				return a.deviceFail(err)
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": approval})
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name of the token minted (default: the name the client gave)")
	cmd.Flags().StringArrayVar(&projects, "project", nil, "project slug the token may use (repeatable; default: this machine's own projects)")
	return cmd
}

func (a *App) serverDeviceDenyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deny <code>",
		Short: "Deny a login request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.deviceClient(cmd)
			if err != nil {
				return err
			}
			var res map[string]any
			if err := client.Do(cmd.Context(), http.MethodPost, server.PathDeviceDeny, map[string]string{"user_code": args[0]}, &res); err != nil {
				return a.deviceFail(err)
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": res})
			return nil
		},
	}
}

// deviceClient builds the client the device commands act through: the
// clued config's server and token, overridden by the device flags.
func (a *App) deviceClient(cmd *cobra.Command) (*remote.Client, error) {
	cfg := a.sessionConfig()
	if v, _ := cmd.Flags().GetString("server"); v != "" {
		cfg.ServerURL = v
	}
	if v, _ := cmd.Flags().GetString("token"); v != "" {
		cfg.ServerToken = v
	}
	if v, _ := cmd.Flags().GetString("ca-file"); v != "" {
		cfg.ServerCAFile = v
	}
	if v, _ := cmd.Flags().GetBool("insecure"); v {
		cfg.ServerInsecure = true
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, a.sessionBadRequest("--server is required when this machine is not in remote mode (no serverUrl in " + a.sess.configPath + ")")
	}
	if cfg.ServerToken == "" {
		return nil, a.sessionBadRequest("--token is required when this machine holds no token for the server (no serverToken in " + a.sess.configPath + ")")
	}
	client, err := remote.New(cfg)
	if err != nil {
		return nil, a.sessionFail(err)
	}
	return client, nil
}

// deviceFail reports a device command's failure: an unknown code is
// NotFound, the rest a storage error.
func (a *App) deviceFail(err error) error {
	if remote.IsNotFound(err) {
		return a.fail(&service.Error{Kind: service.KindNotFound, Message: err.Error()})
	}
	return a.sessionFail(err)
}

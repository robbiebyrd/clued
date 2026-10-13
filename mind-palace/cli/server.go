package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// tokensFlag is the name of the `server` group's persistent --tokens flag;
// subcommands read it through serverTokensPath.
const tokensFlag = "tokens"

// serverCmd groups the clued-server commands: `run` (server_run.go),
// `init` (server_init.go) and `token`.
func (a *App) serverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "The clued server: remote HTTPS storage for sessions, plans and stories",
		Long: `Commands for operating a clued server. "token" mints, lists, revokes and
extends the bearer tokens clients authenticate with; they live hashed in a
0600 JSON file the server re-reads whenever it changes.`,
	}
	cmd.PersistentFlags().String(tokensFlag, "", "token file (default: $CLUED_SERVER_TOKENS, then ~/.clued-server/tokens.json)")
	cmd.AddCommand(a.serverRunCmd(), a.serverInitCmd(), a.serverTokenCmd())
	return cmd
}

// serverTokensPath resolves the token file: --tokens, then
// $CLUED_SERVER_TOKENS, then ~/.clued-server/tokens.json.
func serverTokensPath(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString(tokensFlag); v != "" {
		return v
	}
	if v := os.Getenv("CLUED_SERVER_TOKENS"); v != "" {
		return v
	}
	return defaultTokensPath()
}

func defaultTokensPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".clued-server", "tokens.json")
}

// serverTokenCmd is `server token create|list|revoke|grant`.
func (a *App) serverTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage the bearer tokens clients present to the server",
	}
	cmd.AddCommand(a.serverTokenCreateCmd(), a.serverTokenListCmd(), a.serverTokenRevokeCmd(), a.serverTokenGrantCmd())
	return cmd
}

func (a *App) serverTokenCreateCmd() *cobra.Command {
	var name, account string
	var projects []string
	cmd := &cobra.Command{
		Use:   "create --name <name> [--project <slug>]... [--account <id>]",
		Short: "Mint a token and print it once; the file keeps only its SHA-256",
		Long: `Creates a token bound to an account and a list of projects and prints the
raw token exactly once: it is not stored and cannot be shown again. The
account defaults to a new random id; pass --account to let several machines
share one. --project '*' grants every project.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--name is required"})
			}
			tf := auth.OpenTokenFile(serverTokensPath(cmd))
			raw, err := tf.Create(name, account, projects)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error(), Cause: err})
			}
			tok, err := findToken(tf, name)
			if err != nil {
				return a.fail(err)
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": map[string]any{
				"name":       tok.Name,
				"account_id": tok.AccountID,
				"projects":   tok.Projects,
				"created":    tok.Created,
				"token":      raw,
				"file":       tf.Path(),
			}})
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "token name (unique within the file)")
	cmd.Flags().StringArrayVar(&projects, "project", nil, "project slug the token may use (repeatable; '*' for all)")
	cmd.Flags().StringVar(&account, "account", "", "account id to bind (default: a new random id)")
	return cmd
}

func (a *App) serverTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the tokens in the file (hashes only, revoked ones included)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := auth.OpenTokenFile(serverTokensPath(cmd)).List()
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindStorage, Message: err.Error(), Cause: err})
			}
			if list == nil {
				list = []auth.Token{}
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": list})
			return nil
		},
	}
}

func (a *App) serverTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <name>",
		Short: "Revoke a token; the server refuses it on the next request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tf := auth.OpenTokenFile(serverTokensPath(cmd))
			if err := tf.Revoke(args[0]); err != nil {
				return a.fail(&service.Error{Kind: service.KindNotFound, Message: err.Error(), Cause: err})
			}
			tok, err := findToken(tf, args[0])
			if err != nil {
				return a.fail(err)
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": tok})
			return nil
		},
	}
}

func (a *App) serverTokenGrantCmd() *cobra.Command {
	var projects []string
	cmd := &cobra.Command{
		Use:   "grant <name> --project <slug>...",
		Short: "Add projects to a token's list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(projects) == 0 {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--project is required"})
			}
			tf := auth.OpenTokenFile(serverTokensPath(cmd))
			if err := tf.Grant(args[0], projects...); err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error(), Cause: err})
			}
			tok, err := findToken(tf, args[0])
			if err != nil {
				return a.fail(err)
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": tok})
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&projects, "project", nil, "project slug to add (repeatable; '*' for all)")
	return cmd
}

// findToken returns the named token from the file after a write.
func findToken(tf *auth.TokenFile, name string) (auth.Token, error) {
	list, err := tf.List()
	if err != nil {
		return auth.Token{}, &service.Error{Kind: service.KindStorage, Message: err.Error(), Cause: err}
	}
	for _, t := range list {
		if t.Name == name {
			return t, nil
		}
	}
	return auth.Token{}, &service.Error{Kind: service.KindNotFound, Message: "no token named " + name}
}

package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/server/stack"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// envPluginVersion is exported by scripts/clued: the plugin's version, which
// names the release whose server image the stack should run.
const envPluginVersion = "CLUED_PLUGIN_VERSION"

// serverInitCmd is `server init`: prepares the Docker Compose stack that
// /clued-setup starts to host the server on this machine.
func (a *App) serverInitCmd() *cobra.Command {
	var o stack.Options
	var image string
	cmd := &cobra.Command{
		Use:   "init [--dir ~/.clued-server] [--proxy nginx|none] [--port 8443] [--host name]... [--name token]",
		Short: "Prepare a Docker Compose stack: the server, MongoDB, an nginx TLS front-end, a CA and a token",
		Long: `Writes the directory a Docker Compose deployment of the clued server runs
from (default ~/.clued-server): docker-compose.yml with the server, its
MongoDB and, by default, an nginx service that terminates TLS on --port;
nginx/clued.conf; mind-palace.config.yaml (plans and stories in MongoDB,
per project); a private CA and a server certificate under tls/ covering
localhost, this host's name and every --host; and a first token in
tokens.json, printed once. Start it with the "up" command of the result and
point the plugin at "server_url" with "ca_file" as serverCaFile.

--proxy none leaves nginx out and publishes the server on 127.0.0.1:--port as
plain HTTP for another terminator (Caddy, Traefik, an nginx of your own, an
ingress). --no-tls skips the CA and uses the tls/cert.pem and tls/key.pem you
supply (mkcert, a real CA). Rerunning regenerates the compose, nginx and
storage files and keeps tls/ and tokens.json; --force reissues the server
certificate under the same CA (kept while it covers every host, so a machine
that trusts it keeps trusting the server); --new-ca replaces the CA too; a
token whose name exists is not minted again. The CA is name-constrained to
the hosts: trusted on a machine (server trust), it vouches for this server
and nothing else.

The server image defaults to the release of the plugin that runs this
command (CLUED_PLUGIN_VERSION, set by scripts/clued) and to :latest
otherwise.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.ServerImage = image
			if o.ServerImage == "" {
				o.ServerImage = stack.DefaultServerImage + ":" + serverImageTag(os.Getenv(envPluginVersion))
			}
			res, err := stack.Init(o)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error(), Cause: err})
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": res})
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Dir, "dir", "", "stack directory (default: ~/.clued-server)")
	f.StringVar(&o.Proxy, "proxy", stack.ProxyNginx, "front-end: nginx terminates TLS, none publishes the server on loopback for another terminator")
	f.IntVar(&o.Port, "port", stack.DefaultPort, "port published on this host")
	f.StringArrayVar(&o.Hosts, "host", nil, "extra name or address the certificate covers (repeatable)")
	f.BoolVar(&o.NoTLS, "no-tls", false, "generate no CA or certificate; tls/cert.pem and tls/key.pem are supplied")
	f.BoolVar(&o.Force, "force", false, "reissue the server certificate even when it exists (the CA is kept while it covers every host)")
	f.BoolVar(&o.NewCA, "new-ca", false, "replace the CA too; every machine that trusted the old one must trust the new one")
	f.StringVar(&o.TokenName, "name", "", "name of the token to mint (default: this host's name)")
	f.StringArrayVar(&o.Projects, "project", nil, "project slug the token may use (repeatable; default '*')")
	f.StringVar(&o.Account, "account", "", "account id the token binds (default: a new one)")
	f.BoolVar(&o.NoToken, "no-token", false, "mint no token")
	f.StringVar(&image, "image", "", "clued server image (default: "+stack.DefaultServerImage+":v<plugin version>)")
	f.StringVar(&o.NginxImage, "nginx-image", stack.DefaultNginxImage, "nginx image")
	f.StringVar(&o.MongoImage, "mongo-image", stack.DefaultMongoImage, "MongoDB image")
	return cmd
}

// serverImageTag is the image tag for a plugin version: v<version>, or
// latest when no version is known.
func serverImageTag(version string) string {
	if version == "" {
		return "latest"
	}
	return "v" + version
}

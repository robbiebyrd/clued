package memory

import (
	"context"
	"errors"
)

// EngineHosted is the name of the hosted engine.
const EngineHosted = "hosted"

// HostedClient is what a clued cloud serves for the hosted engine: one
// completion, paid from the account's monthly allowance and then from
// the user's own Anthropic key. The client side ends here: the cloud
// implements the route (and runs memory.Service.Run itself under its
// llm-summary opt-in); a server that does not serve it answers an error,
// and the run falls back to the free extract engine.
type HostedClient interface {
	Complete(ctx context.Context, prompt, model string) (text string, costUSD float64, err error)
}

// ErrHostedUnavailable is returned by a HostedEngine without a client:
// the hosted engine needs Clued Cloud (remote mode).
var ErrHostedUnavailable = errors.New("the hosted engine needs Clued Cloud: link this machine with /clued-setup")

// HostedEngine is the "hosted" digest.Engine, over a HostedClient.
type HostedEngine struct {
	Client HostedClient
}

// Name is "hosted".
func (HostedEngine) Name() string { return EngineHosted }

// Run asks the cloud for the completion.
func (e HostedEngine) Run(ctx context.Context, prompt, model string) (string, float64, error) {
	if e.Client == nil {
		return "", 0, ErrHostedUnavailable
	}
	return e.Client.Complete(ctx, prompt, model)
}

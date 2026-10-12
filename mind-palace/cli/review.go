package cli

import (
	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/ops"
)

// reviewCmd lists the plans and stories whose work is finished while their
// status is still open, so the user can decide to close them.
func (a *App) reviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "List plans whose stories are all complete and stories whose steps and criteria are all done",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := a.init(ctx); err != nil {
				return a.fail(err)
			}
			r, err := a.palace.Review(ctx)
			if err != nil {
				return a.fail(err)
			}
			return a.emit(&ops.Result{Value: r})
		},
	}
}

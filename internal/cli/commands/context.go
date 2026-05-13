package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/neeldholiya04/nexus/internal/app"
)

func NewContextCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var (
		personaID     string
		stableBudget  int
		dynamicBudget int
		stableLimit   int
		dynamicLimit  int
		jsonOut       bool
	)

	cmd := &cobra.Command{
		Use:   "context <intent>",
		Short: "Compose stable and dynamic memory context for an intent",
		Long: `Compose a Nexus context block for an AI tool.

The context composer keeps stable memories separate from dynamic current-work
memories, then returns a budgeted block that can be injected into an AI session.`,
		Args:    cobra.MinimumNArgs(1),
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runContext(cmd.Context(), d, strings.Join(args, " "), app.ContextOptions{
				PersonaID:          personaID,
				StableTokenBudget:  stableBudget,
				DynamicTokenBudget: dynamicBudget,
				StableLimit:        stableLimit,
				DynamicLimit:       dynamicLimit,
			}, jsonOut)
		},
	}

	cmd.Flags().StringVar(&personaID, "persona", "", "Optional persona ID to filter dynamic memories")
	cmd.Flags().IntVar(&stableBudget, "stable-budget", 400, "Approximate stable-memory token budget")
	cmd.Flags().IntVar(&dynamicBudget, "dynamic-budget", 1100, "Approximate dynamic-memory token budget")
	cmd.Flags().IntVar(&stableLimit, "stable-limit", 8, "Maximum stable memories")
	cmd.Flags().IntVar(&dynamicLimit, "dynamic-limit", 12, "Maximum dynamic memories")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runContext(ctx context.Context, d Deps, intent string, opts app.ContextOptions, jsonOut bool) error {
	opts.Intent = intent
	result, err := d.Memory().ComposeContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("context: %w", err)
	}
	if jsonOut {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Println(result.Text)
	return nil
}

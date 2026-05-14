package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/neeldholiya04/nexus/internal/app"
)

func NewPriorsCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var (
		status         bool
		jsonOut        bool
		includeAll     bool
		staleAfterDays int
	)

	cmd := &cobra.Command{
		Use:     "priors",
		Short:   "Inspect archetype prior health",
		Args:    cobra.NoArgs,
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = status
			d := depsFn()
			return runPriorsStatus(cmd.Context(), d, staleAfterDays, includeAll, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "Show archetype prior reinforcement status (default action)")
	cmd.Flags().IntVar(&staleAfterDays, "stale-after-days", 14, "Days before an unreinforced prior is flagged")
	cmd.Flags().BoolVar(&includeAll, "all", false, "Include reinforced priors in the table")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runPriorsStatus(ctx context.Context, d Deps, staleAfterDays int, includeAll, jsonOut bool) error {
	if staleAfterDays <= 0 {
		staleAfterDays = 14
	}
	result, err := d.Memory().PriorsStatus(ctx, app.PriorStatusOptions{
		StaleAfter: time.Duration(staleAfterDays) * 24 * time.Hour,
		IncludeAll: includeAll,
	})
	if err != nil {
		return fmt.Errorf("priors status: %w", err)
	}

	if jsonOut {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Println("Archetype Prior Status")
	fmt.Printf("Total: %d  Reinforced: %d  Pending: %d  Unreinforced: %d  Stale: %d  Contradicted: %d\n",
		result.Total, result.Reinforced, result.Pending, result.Unreinforced, result.Stale, result.Contradicted)
	fmt.Printf("Stale threshold: %d days\n", result.StaleAfterDays)

	if len(result.Items) == 0 {
		fmt.Println("\nNo priors need attention. Use --all to include reinforced priors.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\nSTATUS\tAGE\tSIGNAL\tCONF\tPERSONA\tLAYER\tARCHETYPE\tCONTENT")
	fmt.Fprintln(w, "------\t---\t------\t----\t-------\t-----\t---------\t-------")
	for _, item := range result.Items {
		content := strings.Join(strings.Fields(item.Content), " ")
		if len(content) > 72 {
			content = content[:69] + "..."
		}
		fmt.Fprintf(w, "%s\t%dd\t%s\t%.2f\t%s\t%s\t%s\t%s\n",
			item.Status,
			item.AgeDays,
			formatSignalDays(item.DaysSinceSignal),
			item.Confidence,
			emptyDash(item.PersonaID),
			item.Layer,
			emptyDash(item.ArchetypeID),
			content,
		)
	}
	return w.Flush()
}

func formatSignalDays(days *int) string {
	if days == nil {
		return "-"
	}
	return fmt.Sprintf("%dd", *days)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

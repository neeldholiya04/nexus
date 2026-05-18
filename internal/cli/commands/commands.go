package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/app"
	"github.com/neeldholiya04/nexus/internal/config"
	"github.com/neeldholiya04/nexus/internal/mcp"
	"github.com/neeldholiya04/nexus/internal/memory"
)

type Deps interface {
	Log() *zap.Logger
	Memory() *app.MemoryService
	Config() *config.Config
}

var version = "dev"

// --- Version ---

func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Nexus version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Nexus %s\n", version)
		},
	}
}

// --- Add ---

func NewAddCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var category, tags string
	var layer, personaID string
	var forceNew bool

	cmd := &cobra.Command{
		Use:   "add <content>",
		Short: "Add or update a memory",
		Long: `Add or update a memory in Nexus.

Examples:
  nexus add "Prefers early returns in Go" --category CODING_STYLE --tags "go,style"
  nexus add "Nexus uses SQLite FTS5" --category PROJECT --layer dynamic --tags "nexus,storage"
  nexus add "A separate note" --force-new`,
		Args:    cobra.MinimumNArgs(1),
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runAdd(cmd.Context(), d, strings.Join(args, " "), category, layer, personaID, tags, forceNew)
		},
	}
	cmd.Flags().StringVarP(&category, "category", "c", "FACT",
		"FACT|PREFERENCE|WORKFLOW|PROJECT|CODING_STYLE|INFERRED")
	cmd.Flags().StringVar(&layer, "layer", "auto", "auto|stable|dynamic")
	cmd.Flags().StringVar(&personaID, "persona", "", "Optional persona ID for dynamic memories")
	cmd.Flags().StringVarP(&tags, "tags", "t", "", "Comma-separated tags")
	cmd.Flags().BoolVar(&forceNew, "force-new", false, "Always create a separate memory instead of updating a matching one")
	return cmd
}

func runAdd(ctx context.Context, d Deps, content, categoryStr, layerStr, personaID, tags string, forceNew bool) error {
	if d.Config().App.DryRun {
		action := "add or update"
		if forceNew {
			action = "add a separate"
		}
		fmt.Printf("[DRY RUN] Would %s memory: [%s] %s\nSet NEXUS_APP_DRY_RUN=false to enable writes.\n",
			action,
			categoryStr, content)
		return nil
	}

	cat := memory.Category(categoryStr)
	if !cat.Valid() {
		return fmt.Errorf("invalid category %q. Valid: FACT|PREFERENCE|WORKFLOW|PROJECT|CODING_STYLE|INFERRED", categoryStr)
	}
	var layer memory.Layer
	if strings.TrimSpace(layerStr) != "" && !strings.EqualFold(layerStr, "auto") {
		parsed, ok := memory.ParseLayer(layerStr)
		if !ok {
			return fmt.Errorf("invalid layer %q. Valid: auto|stable|dynamic", layerStr)
		}
		layer = parsed
	}

	var tagSlice []string
	for _, t := range strings.Split(tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tagSlice = append(tagSlice, t)
		}
	}

	result, err := d.Memory().AddMemory(ctx, app.AddMemoryInput{
		Content:   content,
		Category:  cat,
		Layer:     layer,
		PersonaID: personaID,
		Source:    memory.SourceManual,
		Tags:      tagSlice,
		ForceNew:  forceNew,
	})
	if err != nil {
		return fmt.Errorf("add: %w", err)
	}
	if result.EmbeddingErr != nil {
		fmt.Printf("Saved, but embedding failed. Run `nexus embed` to retry.\n")
	}
	status := "Added."
	if result.Updated {
		status = "Updated existing memory."
	}
	fmt.Printf("%s\nID:       %s\nCategory: %s\nLayer:    %s\nContent:  %s\n", status, result.Memory.ID, cat, result.Memory.Layer(), result.Memory.Content)
	if len(tagSlice) > 0 {
		fmt.Printf("Tags:     %s\n", strings.Join(tagSlice, ", "))
	}
	return nil
}

// --- Search ---

func NewSearchCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var limit int
	var category string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:     "search <query>",
		Short:   "Search memory by natural language query",
		Args:    cobra.MinimumNArgs(1),
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runSearch(cmd.Context(), d.Memory(), strings.Join(args, " "), category, limit, jsonOut)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Max results")
	cmd.Flags().StringVarP(&category, "category", "c", "", "Filter by category")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runSearch(ctx context.Context, svc *app.MemoryService, queryText, category string, limit int, jsonOut bool) error {
	opts := app.SearchOptions{Limit: limit}
	if category != "" {
		c := memory.Category(category)
		if !c.Valid() {
			return fmt.Errorf("invalid category %q", category)
		}
		opts.Category = c
	}

	results, err := svc.Search(ctx, queryText, opts)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("No memories found.")
		return nil
	}

	if jsonOut {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tSCORE\tCATEGORY\tLAYER\tCONTENT\tID")
	fmt.Fprintln(w, "-\t-----\t--------\t-----\t-------\t--")
	for i, r := range results {
		content := r.Memory.Content
		if len(content) > 70 {
			content = content[:67] + "..."
		}
		fmt.Fprintf(w, "%d\t%.3f\t%s\t%s\t%s\t%s\n",
			i+1, r.FinalScore, r.Memory.Category, r.Memory.Layer(), content, r.Memory.ID[:8]+"...")
	}
	return w.Flush()
}

// --- List ---

func NewListCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var category string
	var limit int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List stored memories",
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runList(cmd.Context(), d.Memory(), category, limit, jsonOut)
		},
	}
	cmd.Flags().StringVarP(&category, "category", "c", "", "Filter by category")
	cmd.Flags().IntVarP(&limit, "limit", "l", 20, "Max results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runList(ctx context.Context, svc *app.MemoryService, category string, limit int, jsonOut bool) error {
	opts := memory.ListOptions{Limit: limit}
	if category != "" {
		c := memory.Category(category)
		if !c.Valid() {
			return fmt.Errorf("invalid category %q", category)
		}
		opts.Categories = []memory.Category{c}
	}

	memories, err := svc.List(ctx, opts)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(memories) == 0 {
		fmt.Println("No memories found.")
		return nil
	}

	if jsonOut {
		b, _ := json.MarshalIndent(memories, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tCATEGORY\tLAYER\tCONF\tCONTENT\tID")
	fmt.Fprintln(w, "-\t--------\t-----\t----\t-------\t--")
	for i, m := range memories {
		content := m.Content
		if len(content) > 60 {
			content = content[:57] + "..."
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%.2f\t%s\t%s\n",
			i+1, m.Category, m.Layer(), m.Confidence, content, m.ID[:8]+"...")
	}
	_ = w.Flush()
	fmt.Printf("\nTotal: %d\n", len(memories))
	return nil
}

// --- Delete ---

func NewDeleteCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a memory by ID",
		Args:    cobra.ExactArgs(1),
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			if d.Config().App.DryRun {
				fmt.Printf("[DRY RUN] Would delete: %s\n", args[0])
				return nil
			}
			if err := d.Memory().Delete(cmd.Context(), args[0]); err != nil {
				return fmt.Errorf("delete: %w", err)
			}
			fmt.Printf("Deleted: %s\n", args[0])
			return nil
		},
	}
}

// --- Stats ---

func NewStatsCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	return &cobra.Command{
		Use:     "stats",
		Short:   "Show memory store statistics",
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			stats, err := d.Memory().Stats(cmd.Context())
			if err != nil {
				return fmt.Errorf("stats: %w", err)
			}
			b, _ := json.MarshalIndent(stats, "", "  ")
			fmt.Printf("%s\nDry-run: %v\n", b, d.Config().App.DryRun)
			return nil
		},
	}
}

// --- Serve (MCP) ---

func NewServeCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var transport string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Nexus MCP server",
		Long: `Start the Nexus MCP server.

Transports:
  stdio  — Claude Code (default)
  sse    — Claude Desktop (HTTP SSE at localhost:7798)

Examples:
  nexus serve
  nexus serve --transport sse`,
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runServe(cmd.Context(), d, transport)
		},
	}
	cmd.Flags().StringVarP(&transport, "transport", "t", "stdio", "stdio|sse")
	return cmd
}

func runServe(ctx context.Context, d Deps, transport string) error {
	cfg := d.Config()
	if transport != "" {
		cfg.MCP.Transport = transport
	}

	srv, err := mcp.New(d.Memory(), cfg, d.Log())
	if err != nil {
		return fmt.Errorf("serve: init MCP server: %w", err)
	}

	// Graceful shutdown on SIGINT/SIGTERM
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		d.Log().Info("nexus: shutdown signal received")
		cancel()
	}()

	d.Log().Info("nexus: MCP server starting", zap.String("transport", cfg.MCP.Transport))

	switch cfg.MCP.Transport {
	case "stdio":
		return srv.ServeStdio(ctx)
	case "sse":
		return srv.ServeSSE(ctx)
	default:
		return fmt.Errorf("unknown transport %q", cfg.MCP.Transport)
	}
}

// --- Embed (backfill) ---

func NewEmbedCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var batchSize int

	cmd := &cobra.Command{
		Use:     "embed",
		Short:   "Backfill embeddings for memories that lack them",
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runEmbed(cmd.Context(), d, batchSize)
		},
	}
	cmd.Flags().IntVarP(&batchSize, "batch", "b", 20, "Memories per batch")
	return cmd
}

func runEmbed(ctx context.Context, d Deps, batchSize int) error {
	total, err := d.Memory().BackfillEmbeddings(ctx, batchSize, func(total int) {
		fmt.Printf("Embedded %d memories...\n", total)
	})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	fmt.Printf("Done. Total embedded: %d\n", total)
	return nil
}

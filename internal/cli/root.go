package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/app"
	"github.com/neeldholiya04/nexus/internal/cli/commands"
	"github.com/neeldholiya04/nexus/internal/config"
	"github.com/neeldholiya04/nexus/internal/embeddings/ollama"
	"github.com/neeldholiya04/nexus/internal/logger"
	"github.com/neeldholiya04/nexus/internal/retrieval"
	"github.com/neeldholiya04/nexus/internal/storage/sqlite"
)

func Execute() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type deps struct {
	cfg     *config.Config
	log     *zap.Logger
	service *app.MemoryService
}

func (d *deps) Log() *zap.Logger           { return d.log }
func (d *deps) Memory() *app.MemoryService { return d.service }
func (d *deps) Config() *config.Config     { return d.cfg }

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "nexus",
		Short: "Nexus — personal context engine and AI memory layer",
		Long: `Nexus maintains a persistent, queryable memory layer across all your AI sessions.
It eliminates cold starts by remembering your preferences, projects, and workflows.

Dry-run is ON by default. Set NEXUS_APP_DRY_RUN=false to enable writes.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	var d *deps
	initFn := func(cmd *cobra.Command, args []string) error {
		if d != nil {
			return nil
		}
		var err error
		d, err = buildDeps()
		return err
	}
	depsFn := func() commands.Deps { return d }

	root.AddCommand(
		commands.NewVersionCmd(),
		commands.NewInitCmd(initFn, depsFn),
		commands.NewAddCmd(initFn, depsFn),
		commands.NewContextCmd(initFn, depsFn),
		commands.NewResolveCmd(initFn, depsFn),
		commands.NewPriorsCmd(initFn, depsFn),
		commands.NewGenerateCmd(initFn, depsFn),
		commands.NewWorkflowProfileCmd(initFn, depsFn),
		commands.NewLessonsCmd(initFn, depsFn),
		commands.NewSearchCmd(initFn, depsFn),
		commands.NewListCmd(initFn, depsFn),
		commands.NewDeleteCmd(initFn, depsFn),
		commands.NewStatsCmd(initFn, depsFn),
		commands.NewServeCmd(initFn, depsFn),
		commands.NewEmbedCmd(initFn, depsFn),
		commands.NewInferCmd(initFn, depsFn),
		commands.NewIngestCmd(initFn, depsFn),
	)

	return root
}

func buildDeps() (*deps, error) {
	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	log, err := logger.New(logger.Config{
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		OutputPath: cfg.Log.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	dbPath := resolveDBPath(cfg)

	db, err := sqlite.New(sqlite.Config{
		Path:          dbPath,
		MaxOpenConns:  cfg.Storage.MaxOpenConns,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	embedClient := ollama.New(ollama.Config{
		BaseURL: cfg.Ollama.BaseURL,
		Model:   cfg.Ollama.EmbeddingModel,
		Timeout: cfg.Ollama.Timeout,
	}, log)

	pipeline := retrieval.New(db, embedClient, retrieval.Config{
		SemanticWeight:         cfg.Retrieval.SemanticWeight,
		RecencyWeight:          cfg.Retrieval.RecencyWeight,
		CategoryWeight:         cfg.Retrieval.CategoryWeight,
		ConfidenceWeight:       cfg.Retrieval.ConfidenceWeight,
		RecencyHalfLifeDays:    cfg.Retrieval.RecencyHalfLifeDays,
		DefaultLimit:           cfg.Retrieval.DefaultLimit,
		MinConfidenceThreshold: cfg.Retrieval.MinConfidenceThreshold,
	}, log)
	service := app.NewMemoryService(db, pipeline, embedClient, log)

	if cfg.App.DryRun {
		log.Warn("nexus: DRY-RUN mode — writes are disabled")
	}

	return &deps{
		cfg:     cfg,
		log:     log,
		service: service,
	}, nil
}

func resolveDBPath(cfg *config.Config) string {
	dbPath := strings.TrimSpace(cfg.Storage.DBPath)
	if dbPath != "" {
		return expandHomePath(dbPath)
	}

	dataDir := strings.TrimSpace(cfg.Storage.DataDir)
	if dataDir == "" {
		dataDir = filepath.Join(userHomeDir(), ".nexus")
	}
	dataDir = expandHomePath(dataDir)
	return filepath.Join(dataDir, "nexus.db")
}

func expandHomePath(path string) string {
	home := userHomeDir()
	if home != "" {
		path = strings.ReplaceAll(path, "${HOME}", home)
		path = strings.ReplaceAll(path, "$HOME", home)
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
			path = filepath.Join(home, path[2:])
		}
		if path == "/.nexus" || path == `\.nexus` {
			path = filepath.Join(home, ".nexus")
		}
		if strings.HasPrefix(path, "/.nexus/") || strings.HasPrefix(path, `\.nexus\`) {
			path = filepath.Join(home, ".nexus", path[len("/.nexus/"):])
		}
	}
	return os.ExpandEnv(path)
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return os.Getenv("USERPROFILE")
}

func loadDotEnv() {
	candidates := []string{".env"}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, ".env"),
			filepath.Join(filepath.Dir(exeDir), ".env"),
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}

		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		_ = godotenv.Load(abs)
	}
}

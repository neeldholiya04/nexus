package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/neeldholiya04/nexus/internal/app"
)

func NewInitCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var in app.InitProfileInput
	var interactive bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize V0 archetypes, personas, and bootstrap memories",
		Long: `Initialize the V0 profile layer.

Seeds built-in archetypes, creates selected personas, and writes bootstrap
memories for durable preferences plus current dynamic context.`,
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			if interactive {
				var err error
				in, err = promptInitInput(in)
				if err != nil {
					return err
				}
			}
			return runInit(cmd.Context(), d, in)
		},
	}

	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Run the bootstrap wizard prompts")
	cmd.Flags().StringVar(&in.Name, "name", "", "Profile name")
	cmd.Flags().StringVar(&in.Timezone, "timezone", "", "Profile timezone")
	cmd.Flags().StringSliceVar(&in.ArchetypeIDs, "archetype", nil, "Archetype IDs, repeatable or comma-separated")
	cmd.Flags().StringVar(&in.PrimaryLanguage, "primary-language", "", "Bootstrap coding-style answer")
	cmd.Flags().StringVar(&in.ExplanationDepth, "explanation-depth", "", "Bootstrap communication preference")
	cmd.Flags().StringVar(&in.CurrentProject, "current-project", "", "Current main project name")
	cmd.Flags().StringVar(&in.CurrentProjectPath, "current-project-path", "", "Current main project path")
	cmd.Flags().StringVar(&in.ArchitecturePreference, "architecture-preference", "", "Bootstrap architecture preference")
	cmd.Flags().StringVar(&in.CurrentFocus, "current-focus", "", "Current active focus/task")
	return cmd
}

func promptInitInput(in app.InitProfileInput) (app.InitProfileInput, error) {
	reader := bufio.NewReader(os.Stdin)
	var err error
	if in.Name, err = promptDefault(reader, "Name", in.Name); err != nil {
		return in, err
	}
	if in.Timezone, err = promptDefault(reader, "Timezone", in.Timezone); err != nil {
		return in, err
	}
	archetypes := strings.Join(in.ArchetypeIDs, ",")
	if archetypes, err = promptDefault(reader, "Archetypes (comma-separated)", archetypes); err != nil {
		return in, err
	}
	if archetypes != "" {
		in.ArchetypeIDs = strings.Split(archetypes, ",")
	}
	if in.PrimaryLanguage, err = promptDefault(reader, "Primary coding language", in.PrimaryLanguage); err != nil {
		return in, err
	}
	if in.ExplanationDepth, err = promptDefault(reader, "Preferred explanation depth", in.ExplanationDepth); err != nil {
		return in, err
	}
	if in.CurrentProject, err = promptDefault(reader, "Current main project", in.CurrentProject); err != nil {
		return in, err
	}
	if in.CurrentProjectPath, err = promptDefault(reader, "Current project path", in.CurrentProjectPath); err != nil {
		return in, err
	}
	if in.ArchitecturePreference, err = promptDefault(reader, "Architecture preference", in.ArchitecturePreference); err != nil {
		return in, err
	}
	if in.CurrentFocus, err = promptDefault(reader, "Current focus", in.CurrentFocus); err != nil {
		return in, err
	}
	return in, nil
}

func promptDefault(reader *bufio.Reader, label, current string) (string, error) {
	if current != "" {
		fmt.Printf("%s [%s]: ", label, current)
	} else {
		fmt.Printf("%s: ", label)
	}
	text, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return current, nil
	}
	return text, nil
}

func runInit(ctx context.Context, d Deps, in app.InitProfileInput) error {
	if d.Config().App.DryRun {
		fmt.Println("[DRY RUN] Profile not initialized. Set NEXUS_APP_DRY_RUN=false to enable writes.")
		return nil
	}
	result, err := d.Memory().InitializeProfile(ctx, in)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	fmt.Printf("Initialized V0 profile.\nArchetypes: %d\nPersonas:   %d\nInserted:   %d\nUpdated:    %d\n",
		result.ArchetypesSeeded, result.PersonasSeeded, result.MemoriesInserted, result.MemoriesUpdated)
	if len(result.Personas) > 0 {
		fmt.Println("\nPersonas:")
		for _, persona := range result.Personas {
			fmt.Printf("- %s (%s)\n", persona.ID, persona.Name)
		}
	}
	return nil
}

func NewResolveCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "resolve <intent>",
		Short:   "Resolve the active persona for an intent",
		Args:    cobra.MinimumNArgs(1),
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runResolve(cmd.Context(), d, strings.Join(args, " "), jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runResolve(ctx context.Context, d Deps, intent string, jsonOut bool) error {
	result, err := d.Memory().ResolvePersona(ctx, intent)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	if jsonOut {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("Mode: %s\n", result.Mode)
	if result.Primary != "" {
		fmt.Printf("Primary: %s\n", result.Primary)
	}
	if len(result.Secondary) > 0 {
		fmt.Printf("Secondary: %s\n", strings.Join(result.Secondary, ", "))
	}
	if result.Explanation != "" {
		fmt.Printf("Why: %s\n", result.Explanation)
	}
	if len(result.Scores) > 0 {
		fmt.Println("Scores:")
		for id, score := range result.Scores {
			fmt.Printf("- %s: %.2f\n", id, score)
		}
	}
	return nil
}

func NewGenerateCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var (
		intent  string
		outDir  string
		print   bool
		persona string
	)
	cmd := &cobra.Command{
		Use:     "generate",
		Short:   "Generate CLAUDE.md and AGENTS.md from Nexus context",
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runGenerate(cmd.Context(), d, intent, persona, outDir, print)
		},
	}
	cmd.Flags().StringVar(&intent, "intent", "working in this repository", "Intent used for context composition")
	cmd.Flags().StringVar(&persona, "persona", "", "Optional persona ID")
	cmd.Flags().StringVar(&outDir, "out", ".", "Output directory")
	cmd.Flags().BoolVar(&print, "print", false, "Print instead of writing files")
	return cmd
}

func runGenerate(ctx context.Context, d Deps, intent, persona, outDir string, printOnly bool) error {
	result, err := d.Memory().ComposeContext(ctx, app.ContextOptions{
		Intent:    intent,
		PersonaID: persona,
	})
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	content := generatedAgentInstructions(result.Text)
	if printOnly {
		fmt.Println(content)
		return nil
	}
	if d.Config().App.DryRun {
		fmt.Println("[DRY RUN] Instruction files not written. Set NEXUS_APP_DRY_RUN=false to enable writes.")
		return nil
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("generate: create output dir: %w", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		path := filepath.Join(outDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("generate: write %s: %w", name, err)
		}
		fmt.Printf("Wrote %s\n", path)
	}
	return nil
}

func NewWorkflowProfileCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "workflow-profile <persona_id>",
		Short:   "Show a stable/dynamic workflow profile for a persona",
		Args:    cobra.ExactArgs(1),
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			profile, err := d.Memory().WorkflowProfile(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("workflow-profile: %w", err)
			}
			if jsonOut {
				b, _ := json.MarshalIndent(profile, "", "  ")
				fmt.Println(string(b))
				return nil
			}
			fmt.Println(profile.Text)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func NewLessonsCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var (
		outPath string
		print   bool
		limit   int
	)
	cmd := &cobra.Command{
		Use:     "lessons",
		Short:   "Generate LESSONS.md from current Nexus memory",
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			text, err := d.Memory().GenerateLessons(cmd.Context(), limit)
			if err != nil {
				return fmt.Errorf("lessons: %w", err)
			}
			if print {
				fmt.Print(text)
				return nil
			}
			if d.Config().App.DryRun {
				fmt.Println("[DRY RUN] LESSONS.md not written. Set NEXUS_APP_DRY_RUN=false to enable writes.")
				return nil
			}
			if outPath == "" {
				outPath = "LESSONS.md"
			}
			if err := os.WriteFile(outPath, []byte(text), 0o644); err != nil {
				return fmt.Errorf("lessons: write %s: %w", outPath, err)
			}
			fmt.Printf("Wrote %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "LESSONS.md", "Output path")
	cmd.Flags().BoolVar(&print, "print", false, "Print instead of writing")
	cmd.Flags().IntVar(&limit, "limit", 30, "Number of recent memories to include")
	return cmd
}

func NewIngestCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	cmd := NewInferCmd(initFn, depsFn)
	cmd.Use = "ingest"
	cmd.Short = "Ingest a conversation file through the inference pipeline"
	return cmd
}

func generatedAgentInstructions(contextText string) string {
	return strings.TrimSpace(fmt.Sprintf(`# Nexus Context

Use this context as durable user/project/workflow memory for this workspace.
Keep it subordinate to the user's latest explicit instruction.

%s
`, contextText)) + "\n"
}

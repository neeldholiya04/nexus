package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/app"
	"github.com/neeldholiya04/nexus/internal/config"
	"github.com/neeldholiya04/nexus/internal/inference"
	_ "modernc.org/sqlite"
)

func NewInferCmd(initFn func(*cobra.Command, []string) error, depsFn func() Deps) *cobra.Command {
	var (
		filePath         string
		providerOverride string
		dryRun           bool
		showPrompt       bool
	)

	cmd := &cobra.Command{
		Use:   "infer",
		Short: "Extract memories from a conversation using an LLM",
		Long: `Extract memories from a conversation file using the configured LLM provider.

Reads a conversation (plain text or JSON export), sends it to the inference
provider, and upserts extracted memories into Nexus.

Upsert semantics: if a similar memory already exists in the same category,
its confidence is bumped and tags are merged instead of creating a duplicate.

Providers (set via NEXUS_INFERENCE_PROVIDER or --provider):
  ollama      local, zero cost, no API key (default)
  lmstudio    local OpenAI-compatible
  anthropic   Claude models
  openai      GPT models
  gemini      Gemini models

Examples:
  nexus infer --file chat.txt
  nexus infer --file chat.txt --provider anthropic
  nexus infer --file chat.txt --dry-run
  nexus infer --file chat.txt --show-prompt`,
		PreRunE: initFn,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := depsFn()
			return runInfer(cmd.Context(), d, filePath, providerOverride, dryRun, showPrompt)
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to conversation file (required)")
	cmd.Flags().StringVarP(&providerOverride, "provider", "p", "",
		"Override inference provider: ollama|lmstudio|anthropic|openai|gemini")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Extract and print without writing to store")
	cmd.Flags().BoolVar(&showPrompt, "show-prompt", false,
		"Print the system prompt and user message before calling the LLM")

	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func mapInferenceConfig(c config.InferenceConfig) inference.Config {
	return inference.Config{
		Provider:    c.Provider,
		Model:       c.Model,
		BaseURL:     c.BaseURL,
		APIKey:      c.APIKey,
		MaxTokens:   c.MaxTokens,
		Temperature: c.Temperature,
		Timeout:     c.Timeout,
	}
}

type inferenceValidator func(inference.Config) error
type inferenceFactory func(inference.Config, *zap.Logger) (inference.Provider, error)

func runInfer(ctx context.Context, d Deps, filePath, providerOverride string, dryRun, showPrompt bool) error {
	return runInferWithFactory(ctx, d, filePath, providerOverride, dryRun, showPrompt, inference.Validate, inference.New)
}

func runInferWithFactory(
	ctx context.Context,
	d Deps,
	filePath, providerOverride string,
	dryRun, showPrompt bool,
	validate inferenceValidator,
	newProvider inferenceFactory,
) error {
	text, err := readConversationFile(filePath)
	if err != nil {
		return fmt.Errorf("infer: read file: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("infer: file is empty: %s", filePath)
	}
	d.Log().Info("infer: file loaded",
		zap.String("file", filePath),
		zap.Int("chars", len(text)),
	)

	infCfg := mapInferenceConfig(d.Config().Inference)
	if providerOverride != "" {
		infCfg.Provider = providerOverride
	}

	if err := validate(infCfg); err != nil {
		return fmt.Errorf("infer: provider config invalid: %w\n\nTip: set NEXUS_INFERENCE_PROVIDER=ollama for local inference (no API key needed)", err)
	}

	provider, err := newProvider(infCfg, d.Log())
	if err != nil {
		return fmt.Errorf("infer: build provider: %w", err)
	}

	d.Log().Info("infer: provider ready",
		zap.String("provider", provider.Name()),
		zap.String("model", provider.ModelID()),
	)

	userMessage := buildUserMessage(text)

	if showPrompt {
		fmt.Printf("\n--- SYSTEM PROMPT -----------------------------------\n%s\n", app.InferSystemPrompt)
		fmt.Printf("\n--- USER MESSAGE ------------------------------------\n%s\n\n", userMessage)
	}

	fmt.Printf("Calling %s (%s)...\n", provider.Name(), provider.ModelID())

	result, err := d.Memory().InferSession(ctx, provider, app.InferSessionInput{
		Text:    text,
		Source:  filePath,
		Tool:    "cli",
		DryRun:  dryRun || d.Config().App.DryRun,
		Timeout: d.Config().Inference.Timeout,
	})
	if err != nil {
		return fmt.Errorf("infer: %w", err)
	}

	d.Log().Info("infer: LLM responded",
		zap.String("provider", result.Provider),
		zap.Int("input_tokens", result.InputTokens),
		zap.Int("output_tokens", result.OutputTokens),
	)

	if len(result.Extracted) == 0 {
		fmt.Println("No memories extracted — conversation may not contain user-specific information.")
		return nil
	}

	fmt.Printf("\nExtracted %d memories:\n\n", len(result.Extracted))
	for i, e := range result.Extracted {
		fmt.Printf("[%d] [%s/%s] (conf: %.2f)\n    %s\n", i+1, e.Category, e.Layer, e.Confidence, e.Content)
		if len(e.Tags) > 0 {
			fmt.Printf("    Tags: %s\n", strings.Join(e.Tags, ", "))
		}
		if len(e.Evidence) > 0 {
			fmt.Printf("    Evidence: %s\n", strings.Join(e.Evidence, " | "))
		}
		fmt.Println()
	}

	fmt.Printf("Tokens: %d in / %d out  |  provider: %s  |  model: %s\n\n",
		result.InputTokens, result.OutputTokens, result.Provider, result.Model)

	if result.DryRun {
		fmt.Println("[DRY RUN] Memories not written. Remove --dry-run or set NEXUS_APP_DRY_RUN=false to persist.")
		return nil
	}

	fmt.Printf("Done. Inserted: %d  |  Updated (deduplicated): %d  |  Skipped: %d\n",
		result.Inserted, result.Updated, result.Skipped)

	return nil
}

func readConversationFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}

	content := strings.TrimSpace(string(data))
	switch strings.ToLower(filepath.Ext(path)) {
	case ".db", ".sqlite", ".sqlite3":
		if extracted, err := extractTextFromSQLite(path); err == nil && extracted != "" {
			return extracted, nil
		}
	}

	if len(content) > 0 && (content[0] == '{' || content[0] == '[') {
		if extracted, err := extractTextFromJSON(data); err == nil && extracted != "" {
			return extracted, nil
		}
	}

	return content, nil
}

func extractTextFromSQLite(path string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer db.Close()

	tables, err := sqliteTables(db)
	if err != nil {
		return "", err
	}
	for _, table := range tables {
		columns, err := sqliteColumns(db, table)
		if err != nil {
			continue
		}
		textCol := firstMatchingColumn(columns, "content", "text", "message", "body", "value")
		if textCol == "" {
			continue
		}
		roleCol := firstMatchingColumn(columns, "role", "author", "sender", "speaker")
		orderCol := firstMatchingColumn(columns, "created_at", "timestamp", "time", "date", "id")
		extracted, err := extractMessagesFromSQLiteTable(db, table, roleCol, textCol, orderCol)
		if err == nil && extracted != "" {
			return extracted, nil
		}
	}
	return "", fmt.Errorf("no message-like table found")
}

func sqliteTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func sqliteColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`PRAGMA table_info(` + quoteIdent(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func extractMessagesFromSQLiteTable(db *sql.DB, table, roleCol, textCol, orderCol string) (string, error) {
	selectRole := "''"
	if roleCol != "" {
		selectRole = quoteIdent(roleCol)
	}
	query := `SELECT ` + selectRole + `, ` + quoteIdent(textCol) + ` FROM ` + quoteIdent(table)
	if orderCol != "" {
		query += ` ORDER BY ` + quoteIdent(orderCol)
	}
	query += ` LIMIT 2000`

	rows, err := db.Query(query)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var role, text sql.NullString
		if err := rows.Scan(&role, &text); err != nil {
			return "", err
		}
		content := strings.TrimSpace(text.String)
		if content == "" {
			continue
		}
		label := strings.TrimSpace(role.String)
		if label == "" {
			label = "MESSAGE"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", strings.ToUpper(label), content))
	}
	return sb.String(), rows.Err()
}

func firstMatchingColumn(columns []string, names ...string) string {
	byLower := make(map[string]string, len(columns))
	for _, column := range columns {
		byLower[strings.ToLower(column)] = column
	}
	for _, name := range names {
		if column, ok := byLower[name]; ok {
			return column
		}
	}
	var sorted []string
	for _, column := range columns {
		sorted = append(sorted, column)
	}
	sort.Strings(sorted)
	for _, column := range sorted {
		lower := strings.ToLower(column)
		for _, name := range names {
			if strings.Contains(lower, name) {
				return column
			}
		}
	}
	return ""
}

func quoteIdent(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func extractTextFromJSON(data []byte) (string, error) {
	var generic struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}

	if err := json.Unmarshal(data, &generic); err != nil {
		return "", err
	}
	if len(generic.Messages) == 0 {
		return "", fmt.Errorf("no messages found")
	}

	var sb strings.Builder
	for _, msg := range generic.Messages {
		sb.WriteString(fmt.Sprintf("[%s]: ", strings.ToUpper(msg.Role)))
		switch v := msg.Content.(type) {
		case string:
			sb.WriteString(v)
		case []any:
			for _, block := range v {
				if m, ok := block.(map[string]any); ok {
					if text, ok := m["text"].(string); ok {
						sb.WriteString(text)
					}
				}
			}
		}
		sb.WriteString("\n\n")
	}
	return sb.String(), nil
}

func buildUserMessage(text string) string {
	return app.BuildInferUserMessage(text, 12000)
}

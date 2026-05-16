package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/app"
	"github.com/neeldholiya04/nexus/internal/config"
	"github.com/neeldholiya04/nexus/internal/inference"
	"github.com/neeldholiya04/nexus/internal/memory"
)

type Server struct {
	mcp *server.MCPServer
	mem *app.MemoryService
	cfg *config.Config
	log *zap.Logger
}

func New(mem *app.MemoryService, cfg *config.Config, log *zap.Logger) (*Server, error) {
	s := &Server{
		mem: mem,
		cfg: cfg,
		log: log,
	}

	mcpServer := server.NewMCPServer(
		cfg.MCP.ServerName,
		cfg.MCP.ServerVersion,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, false),
	)

	if err := s.registerTools(mcpServer); err != nil {
		return nil, fmt.Errorf("mcp: register tools: %w", err)
	}

	s.mcp = mcpServer
	return s, nil
}

func (s *Server) ServeStdio(ctx context.Context) error {
	s.log.Info("mcp: starting stdio server")
	return server.ServeStdio(s.mcp)
}

func (s *Server) ServeSSE(ctx context.Context) error {
	s.log.Info("mcp: starting SSE server", zap.String("addr", s.cfg.MCP.SSEAddr))
	sseServer := server.NewSSEServer(s.mcp,
		server.WithBaseURL("http://"+s.cfg.MCP.SSEAddr),
	)
	return sseServer.Start(s.cfg.MCP.SSEAddr)
}

func (s *Server) registerTools(mcpServer *server.MCPServer) error {
	tools := []struct {
		tool    mcpgo.Tool
		handler server.ToolHandlerFunc
	}{
		{s.searchTool(), s.handleSearch},
		{s.contextTool(), s.handleContext},
		{s.projectContextTool(), s.handleProjectContext},
		{s.workflowProfileTool(), s.handleWorkflowProfile},
		{s.addTool(), s.handleAdd},
		{s.inferTool(), s.handleInfer},
		{s.listTool(), s.handleList},
		{s.deleteTool(), s.handleDelete},
		{s.statsTool(), s.handleStats},
	}

	for _, t := range tools {
		mcpServer.AddTool(t.tool, t.handler)
	}

	return nil
}

// ---- Tool definitions ----

func (s *Server) searchTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_search",
		mcpgo.WithDescription(
			"Search Nexus memory for relevant context. "+
				"Use this before answering questions about the user's preferences, "+
				"projects, workflows, or coding style.",
		),
		mcpgo.WithString("query",
			mcpgo.Required(),
			mcpgo.Description("Natural language search query"),
		),
		mcpgo.WithNumber("limit",
			mcpgo.Description("Maximum number of results to return (default: 10)"),
		),
		mcpgo.WithString("category",
			mcpgo.Description("Filter by category: FACT|PREFERENCE|WORKFLOW|PROJECT|CODING_STYLE|INFERRED"),
		),
	)
}

func (s *Server) contextTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_get_context",
		mcpgo.WithDescription(
			"Compose a stable + dynamic Nexus context block for an intent. "+
				"Use this when an AI client needs ready-to-inject user/project/workflow context.",
		),
		mcpgo.WithString("intent",
			mcpgo.Required(),
			mcpgo.Description("The current user intent or task"),
		),
		mcpgo.WithString("persona_id",
			mcpgo.Description("Optional persona ID used to filter dynamic memories"),
		),
		mcpgo.WithNumber("stable_budget",
			mcpgo.Description("Approximate stable-memory token budget (default: 400)"),
		),
		mcpgo.WithNumber("dynamic_budget",
			mcpgo.Description("Approximate dynamic-memory token budget (default: 1100)"),
		),
	)
}

func (s *Server) projectContextTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_get_project_context",
		mcpgo.WithDescription("Detect project/workspace context and compose a Nexus context block for it."),
		mcpgo.WithString("cwd",
			mcpgo.Description("Optional current working directory or repository path"),
		),
		mcpgo.WithString("intent",
			mcpgo.Description("Optional current task intent within the project"),
		),
	)
}

func (s *Server) workflowProfileTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_get_workflow_profile",
		mcpgo.WithDescription("Return the stable/dynamic workflow profile for a persona."),
		mcpgo.WithString("persona_id",
			mcpgo.Required(),
			mcpgo.Description("Persona ID such as sre_infra, startup_builder, fullstack_dev"),
		),
	)
}

func (s *Server) addTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_add",
		mcpgo.WithDescription(
			"Add or update a memory in Nexus. "+
				"Use this to persist important facts, preferences, or context "+
				"that should be available in future sessions. "+
				"By default, matching memories are updated instead of duplicated.",
		),
		mcpgo.WithString("content",
			mcpgo.Required(),
			mcpgo.Description("The memory content to store"),
		),
		mcpgo.WithString("category",
			mcpgo.Required(),
			mcpgo.Description("Category: FACT|PREFERENCE|WORKFLOW|PROJECT|CODING_STYLE|INFERRED"),
		),
		mcpgo.WithString("layer",
			mcpgo.Description("Optional layer: stable|dynamic. When omitted, Nexus chooses a default by category."),
		),
		mcpgo.WithString("persona_id",
			mcpgo.Description("Optional persona ID for dynamic memories"),
		),
		mcpgo.WithString("tags",
			mcpgo.Description("Comma-separated tags for this memory (e.g. 'go,sre,nexus')"),
		),
		mcpgo.WithBoolean("force_new",
			mcpgo.Description("When true, always create a separate memory instead of updating a matching one"),
		),
	)
}

func (s *Server) inferTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_infer",
		mcpgo.WithDescription("Run LLM memory extraction over a transcript and upsert extracted memories."),
		mcpgo.WithString("transcript",
			mcpgo.Required(),
			mcpgo.Description("Conversation transcript text"),
		),
		mcpgo.WithString("source",
			mcpgo.Description("Optional source label or file path"),
		),
		mcpgo.WithString("tool",
			mcpgo.Description("Optional tool/client name, e.g. claude-code, codex, chatgpt"),
		),
		mcpgo.WithString("provider",
			mcpgo.Description("Optional provider override: ollama|lmstudio|anthropic|openai|gemini"),
		),
		mcpgo.WithBoolean("dry_run",
			mcpgo.Description("Extract and show memories without writing them"),
		),
	)
}

func (s *Server) listTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_list",
		mcpgo.WithDescription("List memories stored in Nexus with optional filters."),
		mcpgo.WithString("category",
			mcpgo.Description("Filter by category: FACT|PREFERENCE|WORKFLOW|PROJECT|CODING_STYLE|INFERRED"),
		),
		mcpgo.WithNumber("limit",
			mcpgo.Description("Maximum number of results (default: 20)"),
		),
	)
}

func (s *Server) deleteTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_delete",
		mcpgo.WithDescription("Delete a memory from Nexus by its ID."),
		mcpgo.WithString("id",
			mcpgo.Required(),
			mcpgo.Description("The UUID of the memory to delete"),
		),
	)
}

func (s *Server) statsTool() mcpgo.Tool {
	return mcpgo.NewTool("nexus_stats",
		mcpgo.WithDescription("Show Nexus memory store statistics."),
	)
}

// Tool handlers

func (s *Server) handleSearch(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()

	queryText, ok := args["query"].(string)
	if !ok || queryText == "" {
		return errorResult("query parameter is required"), nil
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	opts := app.SearchOptions{Limit: limit}
	if cat, ok := args["category"].(string); ok && cat != "" {
		c := memory.Category(cat)
		if !c.Valid() {
			return errorResult(fmt.Sprintf("invalid category %q", cat)), nil
		}
		opts.Category = c
	}

	s.log.Info("mcp: nexus_search", zap.String("query", queryText), zap.Int("limit", limit))

	results, err := s.mem.Search(ctx, queryText, opts)
	if err != nil {
		s.log.Error("mcp: search failed", zap.Error(err))
		return errorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return textResult("No matching memories found."), nil
	}

	return textResult(formatSearchResults(results)), nil
}

func (s *Server) handleContext(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()

	intent, ok := args["intent"].(string)
	if !ok || strings.TrimSpace(intent) == "" {
		return errorResult("intent parameter is required"), nil
	}

	opts := app.ContextOptions{Intent: intent}
	if personaID, ok := args["persona_id"].(string); ok {
		opts.PersonaID = personaID
	}
	if budget, ok := args["stable_budget"].(float64); ok && budget > 0 {
		opts.StableTokenBudget = int(budget)
	}
	if budget, ok := args["dynamic_budget"].(float64); ok && budget > 0 {
		opts.DynamicTokenBudget = int(budget)
	}

	result, err := s.mem.ComposeContext(ctx, opts)
	if err != nil {
		s.log.Error("mcp: nexus_get_context failed", zap.Error(err))
		return errorResult(fmt.Sprintf("nexus_get_context failed: %v", err)), nil
	}

	s.log.Info("mcp: nexus_get_context",
		zap.String("intent", intent),
		zap.Int("stable", len(result.Stable)),
		zap.Int("dynamic", len(result.Dynamic)))

	return textResult(result.Text), nil
}

func (s *Server) handleProjectContext(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()

	cwd := ""
	if value, ok := args["cwd"].(string); ok {
		cwd = strings.TrimSpace(value)
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	intent := "project context"
	if value, ok := args["intent"].(string); ok && strings.TrimSpace(value) != "" {
		intent = strings.TrimSpace(value)
	}
	projectName := filepath.Base(cwd)
	if projectName == "." || projectName == string(filepath.Separator) {
		projectName = "current project"
	}
	fullIntent := fmt.Sprintf("Project: %s at %s. %s", projectName, cwd, intent)
	result, err := s.mem.ComposeContext(ctx, app.ContextOptions{Intent: fullIntent})
	if err != nil {
		s.log.Error("mcp: nexus_get_project_context failed", zap.Error(err))
		return errorResult(fmt.Sprintf("nexus_get_project_context failed: %v", err)), nil
	}
	return textResult(result.Text), nil
}

func (s *Server) handleWorkflowProfile(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	personaID, ok := args["persona_id"].(string)
	if !ok || strings.TrimSpace(personaID) == "" {
		return errorResult("persona_id parameter is required"), nil
	}
	profile, err := s.mem.WorkflowProfile(ctx, personaID)
	if err != nil {
		s.log.Error("mcp: nexus_get_workflow_profile failed", zap.Error(err))
		return errorResult(fmt.Sprintf("nexus_get_workflow_profile failed: %v", err)), nil
	}
	return textResult(profile.Text), nil
}

func (s *Server) handleAdd(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.cfg.App.DryRun {
		return textResult("[DRY RUN] Memory not persisted. Set NEXUS_APP_DRY_RUN=false to enable writes."), nil
	}

	args := req.GetArguments()

	content, ok := args["content"].(string)
	if !ok || content == "" {
		return errorResult("content parameter is required"), nil
	}

	catStr, ok := args["category"].(string)
	if !ok || catStr == "" {
		if typeStr, typeOK := args["type"].(string); typeOK {
			catStr = typeStr
		}
	}
	if catStr == "" {
		return errorResult("category or type parameter is required"), nil
	}

	cat := memory.Category(catStr)
	if !cat.Valid() {
		return errorResult(fmt.Sprintf("invalid category %q. Valid: FACT|PREFERENCE|WORKFLOW|PROJECT|CODING_STYLE|INFERRED", catStr)), nil
	}
	var layer memory.Layer
	if layerStr, ok := args["layer"].(string); ok && strings.TrimSpace(layerStr) != "" {
		parsed, valid := memory.ParseLayer(layerStr)
		if !valid {
			return errorResult(fmt.Sprintf("invalid layer %q. Valid: stable|dynamic", layerStr)), nil
		}
		layer = parsed
	}
	personaID := ""
	if value, ok := args["persona_id"].(string); ok {
		personaID = value
	}

	var tags []string
	if t, ok := args["tags"].(string); ok && t != "" {
		for _, tag := range strings.Split(t, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}

	forceNew := false
	if value, ok := args["force_new"].(bool); ok {
		forceNew = value
	}

	result, err := s.mem.AddMemory(ctx, app.AddMemoryInput{
		Content:   content,
		Category:  cat,
		Layer:     layer,
		PersonaID: personaID,
		Source:    memory.SourceMCP,
		Tags:      tags,
		ForceNew:  forceNew,
	})
	if err != nil {
		s.log.Error("mcp: add failed", zap.Error(err))
		return errorResult(fmt.Sprintf("failed to add memory: %v", err)), nil
	}

	s.log.Info("mcp: memory saved", zap.String("id", result.Memory.ID), zap.String("category", string(cat)), zap.Bool("updated", result.Updated))

	status := "Memory added successfully."
	if result.Updated {
		status = "Memory updated successfully."
	}
	msg := fmt.Sprintf("%s\nID: %s\nCategory: %s\nLayer: %s\nContent: %s",
		status, result.Memory.ID, cat, result.Memory.Layer(), result.Memory.Content)
	if result.EmbeddingErr != nil {
		msg += "\nEmbedding: failed; run nexus embed to retry."
	}
	return textResult(msg), nil
}

func (s *Server) handleInfer(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	transcript, ok := args["transcript"].(string)
	if !ok || strings.TrimSpace(transcript) == "" {
		return errorResult("transcript parameter is required"), nil
	}

	cfg := inference.Config{
		Provider:    s.cfg.Inference.Provider,
		Model:       s.cfg.Inference.Model,
		BaseURL:     s.cfg.Inference.BaseURL,
		APIKey:      s.cfg.Inference.APIKey,
		MaxTokens:   s.cfg.Inference.MaxTokens,
		Temperature: s.cfg.Inference.Temperature,
		Timeout:     s.cfg.Inference.Timeout,
	}
	if provider, ok := args["provider"].(string); ok && strings.TrimSpace(provider) != "" {
		cfg.Provider = provider
	}
	if err := inference.Validate(cfg); err != nil {
		return errorResult(fmt.Sprintf("inference provider config invalid: %v", err)), nil
	}
	provider, err := inference.New(cfg, s.log)
	if err != nil {
		return errorResult(fmt.Sprintf("build inference provider failed: %v", err)), nil
	}

	source := "mcp"
	if value, ok := args["source"].(string); ok && strings.TrimSpace(value) != "" {
		source = strings.TrimSpace(value)
	}
	tool := "mcp"
	if value, ok := args["tool"].(string); ok && strings.TrimSpace(value) != "" {
		tool = strings.TrimSpace(value)
	}
	dryRun := s.cfg.App.DryRun
	if value, ok := args["dry_run"].(bool); ok {
		dryRun = value || s.cfg.App.DryRun
	}

	result, err := s.mem.InferSession(ctx, provider, app.InferSessionInput{
		Text:    transcript,
		Source:  source,
		Tool:    tool,
		DryRun:  dryRun,
		Timeout: s.cfg.Inference.Timeout,
	})
	if err != nil {
		s.log.Error("mcp: nexus_infer failed", zap.Error(err))
		return errorResult(fmt.Sprintf("nexus_infer failed: %v", err)), nil
	}
	return textResult(formatInferResult(result)), nil
}

func (s *Server) handleList(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()

	opts := memory.ListOptions{
		Limit: 20,
	}

	if l, ok := args["limit"].(float64); ok && l > 0 {
		opts.Limit = int(l)
	}

	if cat, ok := args["category"].(string); ok && cat != "" {
		c := memory.Category(cat)
		if !c.Valid() {
			return errorResult(fmt.Sprintf("invalid category %q", cat)), nil
		}
		opts.Categories = []memory.Category{c}
	}

	memories, err := s.mem.List(ctx, opts)
	if err != nil {
		s.log.Error("mcp: list failed", zap.Error(err))
		return errorResult(fmt.Sprintf("list failed: %v", err)), nil
	}

	if len(memories) == 0 {
		return textResult("No memories found."), nil
	}

	return textResult(formatMemoryList(memories)), nil
}

func (s *Server) handleDelete(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.cfg.App.DryRun {
		return textResult("[DRY RUN] Memory not deleted. Set NEXUS_APP_DRY_RUN=false to enable writes."), nil
	}

	args := req.GetArguments()

	id, ok := args["id"].(string)
	if !ok || id == "" {
		return errorResult("id parameter is required"), nil
	}

	if err := s.mem.Delete(ctx, id); err != nil {
		s.log.Error("mcp: delete failed", zap.String("id", id), zap.Error(err))
		return errorResult(fmt.Sprintf("delete failed: %v", err)), nil
	}

	s.log.Info("mcp: memory deleted", zap.String("id", id))
	return textResult(fmt.Sprintf("Memory %s deleted successfully.", id)), nil
}

func (s *Server) handleStats(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	stats, err := s.mem.Stats(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("stats failed: %v", err)), nil
	}

	b, _ := json.MarshalIndent(stats, "", "  ")
	return textResult(fmt.Sprintf("Nexus Memory Store Stats:\n%s\nDry-run mode: %v", string(b), s.cfg.App.DryRun)), nil
}

// Formatting helpers

func formatSearchResults(results []memory.RetrievalResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n\n", len(results)))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s/%s (score: %.3f)\n", i+1, r.Memory.Category, r.Memory.Layer(), r.FinalScore))
		sb.WriteString(fmt.Sprintf("    %s\n", r.Memory.Content))
		if len(r.Memory.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("    Tags: %s\n", strings.Join(r.Memory.Tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("    ID: %s\n\n", r.Memory.ID))
	}

	return sb.String()
}

func formatMemoryList(memories []*memory.Memory) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Memories (%d):\n\n", len(memories)))

	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("[%d] [%s] %s\n", i+1, m.Category, m.Content))
		sb.WriteString(fmt.Sprintf("    ID: %s | Layer: %s | Confidence: %.2f | Source: %s\n",
			m.ID, m.Layer(), m.Confidence, m.Source))
		if len(m.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("    Tags: %s\n", strings.Join(m.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatInferResult(result *app.InferSessionResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Extracted %d memories.\n", len(result.Extracted)))
	sb.WriteString(fmt.Sprintf("Tokens: %d in / %d out | provider: %s | model: %s\n",
		result.InputTokens, result.OutputTokens, result.Provider, result.Model))
	if result.DryRun {
		sb.WriteString("[DRY RUN] Memories not written.\n")
	} else {
		sb.WriteString(fmt.Sprintf("Inserted: %d | Updated: %d | Skipped: %d\n", result.Inserted, result.Updated, result.Skipped))
	}
	if len(result.Extracted) == 0 {
		return sb.String()
	}
	sb.WriteString("\n")
	for i, item := range result.Extracted {
		sb.WriteString(fmt.Sprintf("[%d] [%s/%s] conf=%.2f\n", i+1, item.Category, item.Layer, item.Confidence))
		sb.WriteString(fmt.Sprintf("    %s\n", item.Content))
		if len(item.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("    Tags: %s\n", strings.Join(item.Tags, ", ")))
		}
		if len(item.Evidence) > 0 {
			sb.WriteString(fmt.Sprintf("    Evidence: %s\n", strings.Join(item.Evidence, " | ")))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func textResult(text string) *mcpgo.CallToolResult {
	return mcpgo.NewToolResultText(text)
}

func errorResult(msg string) *mcpgo.CallToolResult {
	return mcpgo.NewToolResultError(msg)
}

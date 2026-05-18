# Nexus

Nexus is a local-first personal context engine for AI-assisted work. It stores durable memory about the user, projects, workflows, preferences, and coding style, then retrieves the right context through CLI and MCP tools.

The current V0/MVP is complete in the practical sense: local memory storage, hybrid retrieval, LLM-based memory extraction, profile/persona context, MCP tools, prior health inspection, and live retrieval verification are all implemented.

## What It Does

- Stores structured memories with categories: `FACT`, `PREFERENCE`, `WORKFLOW`, `PROJECT`, `CODING_STYLE`, `INFERRED`
- Separates stable and dynamic context through `metadata.layer`
- Uses SQLite + FTS5 for local persistence
- Uses Ollama embeddings for semantic retrieval
- Composes ready-to-inject context for AI clients
- Extracts memories from transcripts through local or cloud LLM providers
- Serves MCP tools for Claude Code, Claude Desktop, Codex-style clients, and other MCP clients
- Tracks archetype/persona priors and flags stale or contradicted assumptions
- Generates `CLAUDE.md`, `AGENTS.md`, and `LESSONS.md`

## Current Build

The Windows build is kept in:

```powershell
<PATH_TO_PROJECT>\nexus\bin\nexus.exe
```

Rebuild it with:

```powershell
go build -o bin\nexus.exe .\cmd\nexus
```

If Windows says the binary is locked, stop any running `nexus.exe` MCP/server process and rebuild.

## GitHub Releases

Release automation is configured with GitHub Actions and GoReleaser. Push a version tag to publish cross-platform binaries and checksums:

```powershell
git tag -a v0.1.0-alpha.1 -m "Nexus v0.1.0-alpha.1"
git push origin v0.1.0-alpha.1
```

See [Releasing Nexus](docs/release.md) for the full release workflow and MCP setup from downloaded release assets.

## Quick Start

Install prerequisites:

- Go 1.22+
- Ollama
- PowerShell on Windows

Pull the default embedding model:

```powershell
ollama pull nomic-embed-text
```

Copy the environment file and enable writes when ready:

```powershell
Copy-Item .env.example .env
```

In `.env`:

```env
NEXUS_APP_DRY_RUN=false
NEXUS_INFERENCE_PROVIDER=ollama
NEXUS_INFERENCE_MODEL=llama3.2
```

Build:

```powershell
go build -o bin\nexus.exe .\cmd\nexus
```

Initialize the V0 profile:

```powershell
bin\nexus.exe init --name "Your Name" --archetype sre_infra --archetype startup_builder --archetype fullstack_dev
```

Add a memory:

```powershell
bin\nexus.exe add "Prefers concise implementation-first engineering feedback" --category PREFERENCE --layer stable --tags "communication,engineering"
```

Search memory:

```powershell
bin\nexus.exe search "how should engineering feedback be written"
```

Compose context:

```powershell
bin\nexus.exe context "working on Nexus V1"
```

Extract memories from a transcript:

```powershell
bin\nexus.exe infer --file .\conversation.txt --provider ollama
```

## CLI Commands

Primary commands:

- `add` - add or update a memory
- `search` - ranked memory search
- `context` - compose stable + dynamic context for an intent
- `infer` / `ingest` - extract memories from transcripts with an LLM
- `init` - seed V0 archetypes, personas, and bootstrap memories
- `resolve` - resolve the active persona for an intent
- `workflow-profile` - show stable/dynamic profile for a persona
- `priors --status` - inspect archetype prior health
- `generate` - write `CLAUDE.md` and `AGENTS.md`
- `lessons` - write `LESSONS.md`
- `list`, `delete`, `stats`, `embed`, `serve`, `version`

## MCP Tools

Canonical MCP tools:

- `nexus_search`
- `nexus_get_context`
- `nexus_get_project_context`
- `nexus_get_workflow_profile`
- `nexus_add`
- `nexus_infer`
- `nexus_list`
- `nexus_delete`
- `nexus_stats`

Serve over stdio:

```powershell
bin\nexus.exe serve
```

Claude Desktop style config:

```json
{
  "mcpServers": {
    "nexus": {
      "command": "C:\\path\\to\\nexus\\bin\\nexus.exe",
      "args": ["serve"]
    }
  }
}
```

## Configuration

All configuration is env-backed. Important variables:

```env
NEXUS_APP_DRY_RUN=true
NEXUS_STORAGE_DATA_DIR=${HOME}/.nexus
NEXUS_STORAGE_DB_PATH=
NEXUS_OLLAMA_BASE_URL=http://localhost:11434
NEXUS_OLLAMA_EMBEDDING_MODEL=nomic-embed-text
NEXUS_INFERENCE_PROVIDER=ollama
NEXUS_INFERENCE_MODEL=llama3.2
NEXUS_INFERENCE_API_KEY=
NEXUS_INFERENCE_BASE_URL=
NEXUS_MCP_TRANSPORT=stdio
NEXUS_LOG_LEVEL=info
```

Supported inference providers:

- `ollama`
- `lmstudio`
- `anthropic`
- `openai`
- `gemini`

Cloud providers require `NEXUS_INFERENCE_API_KEY` when `NEXUS_APP_DRY_RUN=false`.

## Verification

Compile check:

```powershell
go test ./...
go vet ./...
```

Build:

```powershell
go build -o bin\nexus.exe .\cmd\nexus
```

Run the MCP smoke script:

```powershell
go run .\scripts\mcp_smoke.go
```

Run the live retrieval benchmark:

```powershell
go run .\benchmarks\runner\main.go --version live-v0-local --live --live-queries 8
```

## Docs

- [Local setup](docs/setup/local-setup.md)
- [High-level design](docs/architecture/HLD.md)
- [SQLite ADR](docs/adr/ADR-001-sqlite-pure-go.md)

## V1 Direction

The next meaningful product milestone is automatic capture and retrieval:

1. Observe user activity and AI sessions.
2. Infer durable memories automatically.
3. Retrieve relevant context without manual search.
4. Inject compact context into the right client at the right moment.

That is the point where Nexus moves from "usable local memory engine" to "ambient context layer."

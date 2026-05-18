# Nexus Local Setup

This guide reflects the current V0 implementation. It is not a scaffold-from-scratch guide.

## Prerequisites

- Go 1.22+
- Ollama
- PowerShell on Windows

Verify:

```powershell
go version
ollama --version
```

Pull the embedding model:

```powershell
ollama pull nomic-embed-text
```

Optional local inference model:

```powershell
ollama pull llama3.2
```

## Environment

Copy the sample env file:

```powershell
Copy-Item .env.example .env
```

Safe defaults:

```env
NEXUS_APP_DRY_RUN=true
NEXUS_STORAGE_DATA_DIR=${HOME}/.nexus
NEXUS_OLLAMA_BASE_URL=http://localhost:11434
NEXUS_OLLAMA_EMBEDDING_MODEL=nomic-embed-text
NEXUS_INFERENCE_PROVIDER=ollama
NEXUS_INFERENCE_MODEL=llama3.2
NEXUS_MCP_TRANSPORT=stdio
```

Set this when you want writes:

```env
NEXUS_APP_DRY_RUN=false
```

Cloud inference providers use the generic inference key:

```env
NEXUS_INFERENCE_PROVIDER=anthropic
NEXUS_INFERENCE_API_KEY=...
```

Supported providers:

- `ollama`
- `lmstudio`
- `anthropic`
- `openai`
- `gemini`

## Build

From the repository root:

```powershell
go build -o bin\nexus.exe .\cmd\nexus
```

Expected build path:

```powershell
<PATH_TO_PROJECT>\nexus\bin\nexus.exe
```

If the build fails because `bin\nexus.exe` is in use, stop any running Nexus MCP/server process and rebuild.

## Initialize V0 Profile

```powershell
bin\nexus.exe init `
  --name "Your Name" `
  --archetype sre_infra `
  --archetype startup_builder `
  --archetype fullstack_dev `
  --primary-language Go `
  --explanation-depth "concise but complete" `
  --current-project Nexus `
  --current-project-path "<PATH_TO_PROJECT>\nexus" `
  --current-focus "finishing V0 and preparing V1"
```

Useful archetypes:

- `sre_infra`
- `cs_student`
- `startup_builder`
- `fullstack_dev`
- `ml_ai_engineer`
- `product_manager`

## Basic Workflow

Add memory:

```powershell
bin\nexus.exe add "Prefers implementation-first technical feedback" --category PREFERENCE --layer stable --tags "communication,engineering"
```

Search:

```powershell
bin\nexus.exe search "technical feedback preference"
```

Compose context:

```powershell
bin\nexus.exe context "working on Nexus V1"
```

Infer memories from a transcript:

```powershell
bin\nexus.exe infer --file .\conversation.txt --provider ollama
```

Backfill embeddings:

```powershell
bin\nexus.exe embed
```

Generate agent context files:

```powershell
bin\nexus.exe generate --intent "working in this repository"
bin\nexus.exe lessons
```

Inspect prior health:

```powershell
bin\nexus.exe priors --status
bin\nexus.exe priors --status --all
```

## MCP Setup

Run stdio server:

```powershell
bin\nexus.exe serve
```

Claude Desktop style JSON:

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

## Verification

Compile:

```powershell
go test ./...
go vet ./...
```

Build:

```powershell
go build -o bin\nexus.exe .\cmd\nexus
```

Smoke MCP:

```powershell
go run .\scripts\mcp_smoke.go
```

Live retrieval benchmark:

```powershell
go run .\benchmarks\runner\main.go --version live-v0-local --live --live-queries 8
```

No Go `*_test.go` files should be added unless the project preference changes.

## Data Location

Default database:

```powershell
C:\Users\<you>\.nexus\nexus.db
```

Override with:

```env
NEXUS_STORAGE_DB_PATH=C:\path\to\nexus.db
```

## Troubleshooting

Ollama embedding failures:

```powershell
ollama serve
ollama pull nomic-embed-text
```

Dry run prevents writes:

```env
NEXUS_APP_DRY_RUN=false
```

Cloud provider validation fails:

```env
NEXUS_INFERENCE_PROVIDER=openai
NEXUS_INFERENCE_API_KEY=...
```

Binary locked during build:

```powershell
Get-Process | Where-Object { $_.Path -like '*\nexus.exe' }
Stop-Process -Id <pid>
go build -o bin\nexus.exe .\cmd\nexus
```

# Releasing Nexus

Nexus releases are published from Git tags through GitHub Actions and GoReleaser.

## What the release workflow does

When a tag matching `v*.*.*` is pushed, `.github/workflows/release.yml`:

1. Checks out the full Git history and tags.
2. Installs the Go version from `go.mod`.
3. Runs `go test ./...` and `go vet ./...`.
4. Runs GoReleaser.
5. Publishes a GitHub Release with Linux, macOS, and Windows archives plus `checksums.txt`.

Tags with prerelease suffixes, such as `v0.1.0-alpha.1` or `v0.1.0-rc.1`, are marked as GitHub prereleases automatically.

## Using a release with MCP

Release assets are self-contained Nexus binaries. Users do not need to clone the repository unless they want to build from source.

Download the asset for your operating system and CPU:

- Windows: `nexus_<version>_windows_amd64.zip` or `nexus_<version>_windows_arm64.zip`
- macOS: `nexus_<version>_darwin_amd64.tar.gz` or `nexus_<version>_darwin_arm64.tar.gz`
- Linux: `nexus_<version>_linux_amd64.tar.gz` or `nexus_<version>_linux_arm64.tar.gz`

Put the extracted `nexus` or `nexus.exe` somewhere stable, for example:

```powershell
C:\Tools\nexus\nexus.exe
```

Nexus stores memory locally. By default each OS user gets a separate SQLite database:

```text
~/.nexus/nexus.db
```

Override it only when you intentionally want a different store:

```env
NEXUS_STORAGE_DATA_DIR=${HOME}/.nexus
NEXUS_STORAGE_DB_PATH=
```

Set up local embeddings with Ollama:

```powershell
ollama pull nomic-embed-text
```

Release binaries default to `NEXUS_APP_ENVIRONMENT=production` and `NEXUS_APP_DRY_RUN=false`. Create a `.env` file next to the binary, or set variables in your shell, only when you want to override the defaults. Do not copy `.env.example` unchanged for normal release usage; it is the safe source/development template.

```env
NEXUS_STORAGE_DATA_DIR=${HOME}/.nexus
NEXUS_OLLAMA_BASE_URL=http://localhost:11434
NEXUS_OLLAMA_EMBEDDING_MODEL=nomic-embed-text
NEXUS_INFERENCE_PROVIDER=ollama
NEXUS_INFERENCE_MODEL=llama3.2
NEXUS_MCP_TRANSPORT=stdio
```

Source builds default to `NEXUS_APP_ENVIRONMENT=development` and `NEXUS_APP_DRY_RUN=true` unless overridden. Release builds flip those defaults at build time. Users can still opt back into dry-run mode with `NEXUS_APP_DRY_RUN=true`.

Initialize the local profile:

```powershell
C:\Tools\nexus\nexus.exe init --name "Your Name" --archetype fullstack_dev --archetype startup_builder
```

Run the MCP server directly to verify it starts:

```powershell
C:\Tools\nexus\nexus.exe serve
```

Configure your MCP client to start Nexus over stdio. Claude Desktop style JSON:

```json
{
  "mcpServers": {
    "nexus": {
      "command": "C:\\Tools\\nexus\\nexus.exe",
      "args": ["serve"]
    }
  }
}
```

After restarting the MCP client, the canonical tools should be available:

- `nexus_search`
- `nexus_get_context`
- `nexus_get_project_context`
- `nexus_get_workflow_profile`
- `nexus_add`
- `nexus_infer`
- `nexus_list`
- `nexus_delete`
- `nexus_stats`

First useful MCP prompt:

```text
Use nexus_add to remember that I prefer concise implementation-first engineering feedback.
Then use nexus_get_context for: working on my current coding project.
```

## First release

Use an alpha tag while Nexus is still settling its public API and storage expectations:

```powershell
git status --short
go test ./...
go vet ./...
git tag -a v0.1.0-alpha.1 -m "Nexus v0.1.0-alpha.1"
git push origin v0.1.0-alpha.1
```

The pushed tag starts the release workflow. Watch it under the repository's Actions tab.

## Stable release

When the CLI and MCP surface feel stable enough for broad use:

```powershell
git tag -a v0.1.0 -m "Nexus v0.1.0"
git push origin v0.1.0
```

## Local release dry run

If GoReleaser is installed locally, verify the config without publishing:

```powershell
goreleaser check
goreleaser release --snapshot --clean
```

Generated local release artifacts go into `dist/`, which is ignored by Git.

## Versioning policy

- Patch release: bug fixes only, for example `v0.1.1`.
- Minor release: new user-visible commands, MCP tools, or storage behavior, for example `v0.2.0`.
- Prerelease: public testing before a stable tag, for example `v0.2.0-alpha.1`.

Do not move or replace a published tag unless the release failed before anyone could reasonably consume it. Prefer a new patch tag for fixes.

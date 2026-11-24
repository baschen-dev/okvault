# okvault tasks. On Windows run `just win::<task>` (PowerShell 7).

# Windows recipes (PowerShell 7)
mod win

set shell := ["bash", "-cu"]

vault := "vault"

default:
    @just --list

# Build the binary into bin/
build:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/okvault ./cmd/okvault

# Unit, MCP and stdio smoke tests
test:
    go vet ./...
    go test ./...

# OKF conformance + hygiene check of the vault
lint:
    go run ./cmd/okvault lint --vault {{vault}}

# Regenerate okvault-owned index.md files
index:
    go run ./cmd/okvault index --vault {{vault}}

# Install okvault into $GOPATH/bin
install:
    go install ./cmd/okvault

# Register okvault with Claude Code (user scope) for this vault
claude-add:
    claude mcp add --scope user okvault -- "$(go env GOPATH)/bin/okvault" serve --vault "$(pwd)/{{vault}}" --author claude-code

# Build the Obsidian plugin and install it into the vault (enable it in Obsidian: Settings > Community plugins)
plugin:
    cd obsidian-plugin && npm ci && npm run build
    mkdir -p {{vault}}/.obsidian/plugins/okvault
    cp obsidian-plugin/main.js obsidian-plugin/manifest.json obsidian-plugin/styles.css {{vault}}/.obsidian/plugins/okvault/

# Test the Obsidian plugin's MCP client against a real `okvault serve`
plugin-test:
    cd obsidian-plugin && npm ci && npm run check && npm test

# Set the plugin's version (manifest, package, versions.json) before tagging vX.Y.Z
plugin-version version:
    cd obsidian-plugin && node version-bump.mjs {{version}}

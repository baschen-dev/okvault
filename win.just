# Windows recipes (PowerShell 7): just win::<task>
set shell := ["pwsh", "-NoProfile", "-Command"]

vault := "vault"

build:
    $v = (git describe --tags --always --dirty 2>$null) ?? 'dev'; go build -ldflags "-X main.version=$v" -o bin/okvault.exe ./cmd/okvault

test:
    go vet ./...; if ($LASTEXITCODE) { exit $LASTEXITCODE }; go test ./...

lint:
    go run ./cmd/okvault lint --vault {{vault}}

index:
    go run ./cmd/okvault index --vault {{vault}}

install:
    go install ./cmd/okvault

claude-add:
    claude mcp add --scope user okvault -- "$(go env GOPATH)\bin\okvault.exe" serve --vault "$((Resolve-Path {{vault}}).Path)" --author claude-code

plugin:
    Push-Location obsidian-plugin; npm ci; if ($LASTEXITCODE) { exit $LASTEXITCODE }; npm run build; $c = $LASTEXITCODE; Pop-Location; if ($c) { exit $c }; New-Item -ItemType Directory -Force {{vault}}/.obsidian/plugins/okvault | Out-Null; Copy-Item obsidian-plugin/main.js, obsidian-plugin/manifest.json, obsidian-plugin/styles.css {{vault}}/.obsidian/plugins/okvault/

plugin-test:
    Push-Location obsidian-plugin; npm ci; if ($LASTEXITCODE) { exit $LASTEXITCODE }; npm run check; if ($LASTEXITCODE) { exit $LASTEXITCODE }; npm test; $c = $LASTEXITCODE; Pop-Location; exit $c

plugin-version version:
    Push-Location obsidian-plugin; node version-bump.mjs {{version}}; $c = $LASTEXITCODE; Pop-Location; exit $c

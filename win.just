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

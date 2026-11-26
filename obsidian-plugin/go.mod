// Not Go code: this marks obsidian-plugin/ as a module of its own, so the
// repository's `go test ./...`, `go vet ./...` and `go install` leave out the
// plugin and its node_modules, where npm packages can ship Go files.
module github.com/baschen-dev/okvault/obsidian-plugin

go 1.27.4

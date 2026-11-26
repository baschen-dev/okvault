package main

import "runtime/debug"

func init() {
	version = resolveVersion(version, debug.ReadBuildInfo)
}

// resolveVersion picks the version okvault reports. A version stamped with
// -ldflags (just build: git describe) wins. Otherwise it is the module version
// Go recorded in the binary, which `go install .../cmd/okvault@v0.5.0` sets to
// v0.5.0 (and a build in a git checkout to a pseudo-version). Failing that, a
// local build reports the commit it was built from, as dev-<commit>[-dirty].
func resolveVersion(stamped string, read func() (*debug.BuildInfo, bool)) string {
	if stamped != "dev" && stamped != "" {
		return stamped
	}
	bi, ok := read()
	if !ok || bi == nil {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return "dev-" + rev + dirty
}

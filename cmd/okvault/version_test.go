package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	info := func(version string, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/baschen-dev/okvault", Version: version}, Settings: settings}, true
		}
	}
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "4a4da2531d0503d207595dd77cb2b05370104812"}
	dirty := debug.BuildSetting{Key: "vcs.modified", Value: "true"}
	none := func() (*debug.BuildInfo, bool) { return nil, false }

	for _, c := range []struct {
		name, stamped string
		read          func() (*debug.BuildInfo, bool)
		want          string
	}{
		{"ldflags win", "v0.5.0-3-gabc1234", info("v0.4.4"), "v0.5.0-3-gabc1234"},
		{"go install @version", "dev", info("v0.5.0"), "v0.5.0"},
		{"build in a checkout gets a pseudo-version", "dev", info("v0.5.1-0.20261126004203-4a4da2531d05+dirty"), "v0.5.1-0.20261126004203-4a4da2531d05+dirty"},
		{"local build: the commit", "dev", info("(devel)", rev), "dev-4a4da2531d05"},
		{"local build with edits", "dev", info("(devel)", rev, dirty), "dev-4a4da2531d05-dirty"},
		{"no vcs info", "dev", info("(devel)"), "dev"},
		{"no build info", "dev", none, "dev"},
	} {
		if got := resolveVersion(c.stamped, c.read); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Package version holds build information. Release builds set it through -ldflags (see .goreleaser.yaml).
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set by the linker, for example -X github.com/klahio/kubectl-isvc/internal/version.Version=v0.1.0.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String is the one-line version shown by `kubectl isvc version` and `--version`.
func String() string {
	commit, date := Commit, Date
	// A `go install` build has no linker flags, but Go records the commit it was built from.
	if commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					commit = s.Value
				case "vcs.time":
					date = s.Value
				}
			}
			if Version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
				Version = info.Main.Version
			}
		}
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	s := Version
	if commit != "" {
		s += " (commit " + commit
		if date != "" {
			s += ", built " + date
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s/%s", s, runtime.GOOS, runtime.GOARCH)
}

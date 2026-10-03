// Package buildinfo describes the running binary, populated at build time.
package buildinfo

var (
	Version = "dev"
	Commit  = ""
	BuiltAt = ""
)

type Manifest struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}

func Current() Manifest {
	return Manifest{Version: Version, Commit: Commit, BuiltAt: BuiltAt}
}

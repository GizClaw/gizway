// Package buildinfo exposes immutable release metadata injected at link time.
package buildinfo

import "strings"

var (
	Version   = "devel"
	Revision  = "unknown"
	BuildTime = "unknown"
)

// Info is the normalized metadata embedded in a process.
type Info struct {
	Version   string
	Revision  string
	BuildTime string
}

// Current returns non-empty metadata even for local, unversioned builds.
func Current() Info {
	return Info{
		Version:   fallback(Version, "devel"),
		Revision:  fallback(Revision, "unknown"),
		BuildTime: fallback(BuildTime, "unknown"),
	}
}

func fallback(value, replacement string) string {
	if strings.TrimSpace(value) == "" {
		return replacement
	}
	return value
}

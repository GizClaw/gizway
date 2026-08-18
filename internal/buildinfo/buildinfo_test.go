package buildinfo

import "testing"

func TestCurrentNormalizesReleaseMetadata(t *testing.T) {
	originalVersion, originalRevision, originalBuildTime := Version, Revision, BuildTime
	t.Cleanup(func() { Version, Revision, BuildTime = originalVersion, originalRevision, originalBuildTime })

	Version, Revision, BuildTime = "v1.2.3", "0123456789abcdef", "2026-08-18T00:00:00Z"
	if got := Current(); got != (Info{"v1.2.3", "0123456789abcdef", "2026-08-18T00:00:00Z"}) {
		t.Fatalf("Current() = %#v", got)
	}

	Version, Revision, BuildTime = "", "  ", ""
	if got := Current(); got != (Info{"devel", "unknown", "unknown"}) {
		t.Fatalf("Current() fallback = %#v", got)
	}
}

// Package version holds build metadata for the gochat binaries.
//
// The values are placeholders by default so that `go build` and `go run`
// work out of the box; release builds override them with -ldflags, e.g.:
//
//	go build -ldflags "\
//	  -X github.com/Satyam-1o/gochat/internal/version.Version=v0.1.0 \
//	  -X github.com/Satyam-1o/gochat/internal/version.Commit=$(git rev-parse --short HEAD) \
//	  -X github.com/Satyam-1o/gochat/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
//	  ./cmd/gochat
package version

import "fmt"

var (
	// Version is the semantic version, e.g. "v0.1.0" or "dev".
	Version = "dev"
	// Commit is the short git commit the binary was built from.
	Commit = "none"
	// Date is the build timestamp (RFC 3339, UTC).
	Date = "unknown"
)

// String returns a human-readable, single-line version summary.
func String() string {
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, Date)
}

// Package version holds build information. Values are overridden at build time via -ldflags.
package version

var (
	// Version is the release version (semver) or "dev".
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// UpdateRepo is the GitHub repo releases are fetched from (D15). Empty = updates disabled (forks).
	UpdateRepo = ""
	// UpdatePubKey is the minisign public key release signatures are checked against.
	UpdatePubKey = ""
	// CrashAfterStart makes `serve` fail right after opening the database – only for testing the
	// update rollback (spec 10 §3). Never set in a release.
	CrashAfterStart = ""
)

// UpdatesEnabled reports whether this build may update itself.
func UpdatesEnabled() bool { return UpdateRepo != "" && UpdatePubKey != "" }

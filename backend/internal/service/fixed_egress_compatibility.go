package service

import (
	"os"
	"strings"
)

// FixedEgressCompatibilityModeEnv is a migration-only bridge for rolling from
// a writer that predates scheduler retirement fences. It keeps the complete
// fixed-egress mutation guards enabled, blocks the CAS migration path, and
// temporarily preserves legacy upstream routing and cache deletion semantics
// until every old binary connected to the shared Redis has exited. The secure
// default is disabled.
const FixedEgressCompatibilityModeEnv = "SUB2API_FIXED_EGRESS_COMPATIBILITY_MODE"

func FixedEgressCompatibilityModeEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(FixedEgressCompatibilityModeEnv)), "true")
}

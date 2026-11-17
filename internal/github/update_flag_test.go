package github

import "flag"

// `make golden` passes -update to every package's tests. Accept it here as
// cmd/percolate does so the shared target does not fail on this package.
var _ = flag.Bool("update", false, "accepted for repository golden updates")

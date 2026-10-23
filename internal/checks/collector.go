package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// RunCollected runs a check on behalf of a remote collector (the agent's
// collector mode, issue #123). It is Run with the gate put where it cannot be
// walked around: a type a collector may not run — a script above all — is
// refused here, whatever the caller checked before, and only the settings a
// network check reads are passed on. The agent validates every definition it
// is handed before it gets this far; this is the second lock on the same door,
// so a mistake in either one alone still runs nothing it should not.
func RunCollected(ctx context.Context, check model.Check, opts Options) model.Result {
	if !check.Type.Collectable() {
		r := failResult(fmt.Sprintf("a collector does not run %q checks", check.Type))
		r.CheckID, r.Timestamp, r.Attempts = check.ID, time.Now(), 1
		return r
	}
	check.Config = check.Config.ForCollector()
	// There is no hardware reader on a collector, and nothing a network check
	// would read from one.
	opts.Hosts = nil
	return Run(ctx, check, opts)
}

package automation

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/google/uuid"
)

// splitTakesA decides which branch of a percentage split a run takes: true = A
// (YesSteps), false = B (NoSteps).
//
// The choice MUST be a pure function of resume-stable inputs, never
// random-at-execution: the engine re-walks the whole steps tree from the root
// on every resume (delay wake, retry, crash recovery, manual RetryRun) and
// re-derives branch decisions — only success/waiting action logs pin a branch
// (see the condition case in executeStepsWithState). A random choice could
// flip sides after the taken branch already produced side effects (a retrying
// send_email may have actually sent). run.ID is stable across resumes (runs
// are refetched by ID) and step IDs come from the run's pinned
// WorkflowVersion, so hash(runID, stepID) is stable for the life of the run
// while still being uniformly distributed across runs.
func splitTakesA(runID uuid.UUID, stepID string, percentA int) bool {
	h := sha256.Sum256([]byte(runID.String() + ":" + stepID))
	v := binary.BigEndian.Uint64(h[:8]) % 100
	return int(v) < percentA
}

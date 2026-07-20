package envelope

import (
	"fmt"
	"sort"
)

// CanaryRow is one stored ciphertext together with the binding it was sealed
// under — everything needed to prove a key still opens real data.
type CanaryRow struct {
	Binding Binding
	Blob    string
}

// Canary opens one real row per distinct key version present in the supplied
// sample, and reports every version that fails.
//
// It takes rows rather than a database handle so this package stays free of
// repository and GORM dependencies; the caller runs the query.
//
// Why real rows and not a generated fixture: sealing a fresh value and opening
// it again proves only that the configured key is internally consistent, which
// is true of ANY 32 valid bytes — including the wrong ones. The failure this
// exists to catch is a KEK that parses perfectly and simply is not the key the
// stored credentials were sealed under (a rotated Railway variable, a keyring
// entry dropped during a paste, a staging key promoted to production). Only a
// row somebody actually wrote can distinguish those.
//
// A sample containing no rows is a clean pass, not a failure: a deployment that
// has never connected a provider has nothing to verify, and refusing to boot
// over it would make the codec's arrival a breaking change for every existing
// workspace.
//
// Versions present in the keyring but absent from the sample are also a pass —
// an operator may legitimately add the next key BEFORE any row uses it, which
// is exactly the safe way to stage a rotation.
func (c *Codec) Canary(rows []CanaryRow) error {
	if !c.Configured() {
		return ErrNotConfigured
	}

	// One row per version is enough, and stopping at the first of each keeps a
	// boot-time check O(versions) rather than O(connections).
	seen := make(map[int]bool)
	failed := make(map[int]error)
	for _, row := range rows {
		version, err := VersionOf(row.Blob)
		if err != nil {
			// A blob we cannot even PARSE is per-row data corruption (a truncated or
			// garbled ciphertext), NOT the key mismatch this check exists to catch.
			// Bricking the whole deployment's boot over one bad row would punish every
			// org for a single corrupt credential; that connection instead surfaces as
			// an open failure the first time it is actually used. Skip it — the canary
			// stays scoped to "does the configured key open well-formed credentials".
			continue
		}
		if seen[version] {
			continue
		}
		seen[version] = true
		if _, err := c.Open(row.Binding, row.Blob); err != nil {
			failed[version] = err
		}
	}

	if len(failed) == 0 {
		return nil
	}

	versions := make([]int, 0, len(failed))
	for v := range failed {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	first := versions[0]
	return fmt.Errorf(
		"stored provider credentials sealed under key version(s) %v could not be opened by the configured INTEGRATION_ENC_KEY (first failure, version %d: %v) — "+
			"the key material has changed without a version bump, or a keyring entry was dropped; restoring the previous key value is the only recovery",
		versions, first, failed[first],
	)
}

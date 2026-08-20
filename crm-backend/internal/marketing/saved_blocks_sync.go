package marketing

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// saved_blocks_sync.go implements SYNCED saved blocks (Klaviyo "universal
// content", Beefree "synced rows"): editing one library block updates every
// email that uses it.
//
// Documents stay MATERIALIZED — an instance holds the block's real content in
// body_json and merely carries Block.Ref pointing at the library row. Nothing in
// compile/validate/preview/plaintext has to resolve a reference, so every
// existing path is untouched; propagation is instead an explicit server-side
// rewrite of the dependents.
//
// The load-bearing detail is the RECOMPILE: body_html_compiled is the SEND
// source. Rewriting body_json alone would update what the builder shows while
// recipients kept receiving the old block — the worst possible outcome for a
// feature whose whole promise is "change it once, everywhere".

// syncedInstanceLimit bounds one propagation. Well above any realistic library
// usage, but a runaway rewrite should stop rather than churn the whole table.
const syncedInstanceLimit = 500

// GetSavedBlockByID loads one saved block, org-scoped.
func (r *Repository) GetSavedBlockByID(ctx context.Context, orgID, id uuid.UUID) (*SavedBlock, error) {
	var row SavedBlock
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&row).Error
	if err != nil {
		if strings.Contains(err.Error(), "record not found") {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// UpdateSavedBlockContent replaces a saved block's content (and marks it synced).
func (r *Repository) UpdateSavedBlockContent(ctx context.Context, orgID, id uuid.UUID, block datatypes.JSON) (bool, error) {
	res := r.db.WithContext(ctx).Model(&SavedBlock{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Updates(map[string]any{"block": block, "synced": true})
	return res.RowsAffected > 0, res.Error
}

// replaceRefInstances swaps every block carrying ref with the library content,
// at the root and one level into columns (the only nesting the model allows).
// The instance keeps its OWN id and ref — the rest of the block is shared.
// Returns the new list and how many instances were replaced.
func replaceRefInstances(blocks []Block, ref string, replacement Block) ([]Block, int) {
	n := 0
	out := make([]Block, len(blocks))
	for i, b := range blocks {
		if b.Ref == ref {
			out[i] = withIdentity(replacement, b, false)
			n++
			continue
		}
		if len(b.Columns) > 0 {
			cols := make([][]Block, len(b.Columns))
			for c, col := range b.Columns {
				inner := make([]Block, len(col))
				for j, sub := range col {
					if sub.Ref == ref {
						// A columns block can never live inside a column, so an
						// instance nested here must not bring one in.
						if replacement.Type == BlockColumns {
							inner[j] = sub
							continue
						}
						inner[j] = withIdentity(replacement, sub, true)
						n++
						continue
					}
					inner[j] = sub
				}
				cols[c] = inner
			}
			b.Columns = cols
		}
		out[i] = b
	}
	return out, n
}

// rootOnly clears the fields the compiler only honours on a ROOT block. A
// condition is the dangerous one: buildMJML wraps only top-level sections, and
// columnInner emits no markers at all — so a "show only if" copied into a column
// is silently ignored and the block ships to EVERY recipient. Section background,
// full-width bleed and padding are root-only for the same structural reason.
func rootOnly(b Block) Block {
	b.Cond = nil
	b.Bg = ""
	b.BgURL = ""
	b.FullWidth = false
	b.PadX, b.PadY = nil, nil
	return b
}

// withIdentity returns the library block wearing the instance's identity, and
// deep-copies nested columns so two instances can never share a slice. nested
// strips the root-only fields the compiler would ignore there.
func withIdentity(lib Block, instance Block, nested bool) Block {
	copied := lib
	if nested {
		copied = rootOnly(copied)
	}
	copied.ID = instance.ID
	copied.Ref = instance.Ref
	if len(lib.Columns) > 0 {
		cols := make([][]Block, len(lib.Columns))
		for c, col := range lib.Columns {
			inner := make([]Block, len(col))
			copy(inner, col)
			// Nested ids must stay unique per instance, or React keys collide
			// across two copies of the same synced block in one document.
			for j := range inner {
				// "sync" marks these as generated: a plain id_c_j could collide with
				// a real block id elsewhere in the document, and duplicate ids make
				// the builder edit the wrong block.
				inner[j].ID = instance.ID + "~sync" + strconv.Itoa(c) + "_" + strconv.Itoa(j)
			}
			cols[c] = inner
		}
		copied.Columns = cols
	}
	return copied
}


// countRefInstances counts a ref's instances in a document.
func countRefInstances(blocks []Block, ref string) int {
	n := 0
	for _, b := range blocks {
		if b.Ref == ref {
			n++
		}
		for _, col := range b.Columns {
			for _, sub := range col {
				if sub.Ref == ref {
					n++
				}
			}
		}
	}
	return n
}

// SyncUsage is one email that uses a synced block.
type SyncUsage struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Instances int       `json:"instances"`
}

// Usage answers "is it safe to change this?" — the question every builder that
// ships synced content puts in front of the update button.
func (h *SavedBlockHandler) Usage(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid saved-block id")
		return
	}
	// The slim projection: a usage count only needs body_json, never the compiled
	// HTML and plain text (the two biggest columns) of every email in the org.
	rows, err := h.repo.ListContentRefsByOrg(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not check where this block is used")
		return
	}
	ref := id.String()
	used := []SyncUsage{}
	total := 0
	for _, row := range rows {
		var doc BlockDocument
		if err := json.Unmarshal(row.BodyJSON, &doc); err != nil {
			continue
		}
		if n := countRefInstances(doc.Blocks, ref); n > 0 {
			used = append(used, SyncUsage{ID: row.ID, Name: row.Name, Instances: n})
			total += n
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"emails": used, "email_count": len(used), "instance_count": total}})
}

// UpdateContent replaces a synced block's content and propagates it to every
// email using it, recompiling each so the change actually reaches recipients.
//
// A dependent that would no longer validate is SKIPPED and reported rather than
// half-written: merge scope is per-email, so a block using company.* can be
// legal in one email and out of scope in another.
func (h *SavedBlockHandler) UpdateContent(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	if h.compiler == nil {
		abortErr(c, http.StatusServiceUnavailable, "the compiler is unavailable — try again shortly")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid saved-block id")
		return
	}
	var req struct {
		Block json.RawMessage `json:"block"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var blk Block
	if err := json.Unmarshal(req.Block, &blk); err != nil || strings.TrimSpace(blk.Type) == "" {
		abortErr(c, http.StatusBadRequest, "invalid block")
		return
	}
	// The library row is the source of truth and carries no instance identity.
	blk.ID = ""
	blk.Ref = ""
	clean, err := json.Marshal(blk)
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid block")
		return
	}

	existing, err := h.repo.GetSavedBlockByID(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not load the saved block")
		return
	}
	if existing == nil {
		abortErr(c, http.StatusNotFound, "saved block not found")
		return
	}

	// The LIBRARY row is written first. If propagation then fails partway, the
	// library still holds the intended content and pressing update again simply
	// re-propagates (the rewrite is idempotent). The other order would leave N
	// emails rewritten to content the library never accepted.
	if _, err := h.repo.UpdateSavedBlockContent(c.Request.Context(), orgID, id, clean); err != nil {
		abortErr(c, http.StatusInternalServerError, "could not update the saved block")
		return
	}
	updated, skipped, err := h.propagate(c.Request.Context(), orgID, id.String(), blk)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not update the emails using this block")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"updated": updated, "skipped": skipped}})
}

// propagate rewrites and recompiles every dependent. Returns the names of the
// emails updated and of those skipped (with the reason).
func (h *SavedBlockHandler) propagate(ctx context.Context, orgID uuid.UUID, ref string, blk Block) ([]string, []string, error) {
	rows, err := h.repo.ListContentByOrg(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	updated := []string{}
	skipped := []string{}
	touched := 0
	for i := range rows {
		row := rows[i]
		var doc BlockDocument
		if err := json.Unmarshal(row.BodyJSON, &doc); err != nil {
			continue
		}
		next, n := replaceRefInstances(doc.Blocks, ref, blk)
		if n == 0 {
			continue
		}
		if touched+n > syncedInstanceLimit {
			skipped = append(skipped, row.Name+" (update limit reached)")
			continue
		}
		doc.Blocks = next

		var scope []string
		if err := json.Unmarshal(row.MergeScope, &scope); err != nil {
			scope = DefaultMergeScope()
		}
		norm := NormalizeMergeScope(scope)
		// The same block can be legal in one email and out of scope in another.
		if errs := ValidateContent(row.Subject, row.Preheader, doc, norm); len(errs) > 0 {
			skipped = append(skipped, row.Name+" (the block uses fields this email doesn't allow)")
			continue
		}
		res, err := h.compiler.Compile(ctx, doc, row.Preheader)
		if err != nil {
			skipped = append(skipped, row.Name+" (it no longer compiles)")
			continue
		}
		// The authoring save refuses oversize content (Gmail clips past ~100KB,
		// and the compliance footer is appended LAST so a clipped send loses the
		// unsubscribe link). Propagation must honour the same gate, or it writes
		// a row its own author can no longer save.
		if res.TooLarge {
			skipped = append(skipped, row.Name+" (it would exceed the 100KB limit)")
			continue
		}
		bodyJSON, err := json.Marshal(doc)
		if err != nil {
			skipped = append(skipped, row.Name)
			continue
		}
		now := time.Now()
		if err := h.repo.UpdateContentBody(ctx, orgID, row.ID, bodyJSON, res, now); err != nil {
			skipped = append(skipped, row.Name+" (could not be saved)")
			continue
		}
		touched += n
		updated = append(updated, row.Name)
	}
	return updated, skipped, nil
}

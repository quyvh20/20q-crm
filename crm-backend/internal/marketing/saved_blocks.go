package marketing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// SavedBlock is one reusable builder block (Klaviyo "saved blocks"): any single
// Block — including a columns block with its nested content — stored org-wide.
// The JSONB holds exactly the wire Block shape; like content, the handler
// re-marshals the PARSED struct so unknown fields never persist.
type SavedBlock struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID     uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name      string         `gorm:"type:varchar(120);not null" json:"name"`
	Block     datatypes.JSON `gorm:"type:jsonb;not null" json:"block"`
	// Synced marks a "universal" block: inserting it creates a LINKED instance
	// (Block.Ref = this id) and updating it propagates to every email using it.
	// A non-synced row keeps the original copy-on-insert behaviour.
	Synced    bool           `gorm:"not null;default:false" json:"synced"`
	CreatedBy *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt time.Time      `gorm:"not null;default:now()" json:"created_at"`
}

// TableName pins the table name.
func (SavedBlock) TableName() string { return "marketing_saved_blocks" }

const maxSavedBlocksPerOrg = 200

// ── Repository ───────────────────────────────────────────────────────────────

// CreateSavedBlock stores one saved block.
func (r *Repository) CreateSavedBlock(ctx context.Context, b *SavedBlock) error {
	return r.db.WithContext(ctx).Create(b).Error
}

// ListSavedBlocksByOrg returns the org's saved blocks, newest first.
func (r *Repository) ListSavedBlocksByOrg(ctx context.Context, orgID uuid.UUID) ([]SavedBlock, error) {
	var rows []SavedBlock
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at DESC").Find(&rows).Error
	return rows, err
}

// CountSavedBlocksByOrg supports the per-org cap.
func (r *Repository) CountSavedBlocksByOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&SavedBlock{}).Where("org_id = ?", orgID).Count(&n).Error
	return n, err
}

// DeleteSavedBlockByID removes an org's saved block.
func (r *Repository) DeleteSavedBlockByID(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&SavedBlock{})
	return res.RowsAffected > 0, res.Error
}

// RenameSavedBlockByID updates a saved block's name.
func (r *Repository) RenameSavedBlockByID(ctx context.Context, orgID, id uuid.UUID, name string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&SavedBlock{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Update("name", name)
	return res.RowsAffected > 0, res.Error
}

// ── Handler ──────────────────────────────────────────────────────────────────

// SavedBlockHandler serves the saved-block library under marketing.manage.
type SavedBlockHandler struct {
	repo *Repository
	// compiler recompiles dependents when a synced block changes — nil-tolerant
	// (the sync endpoint 503s rather than writing body_json without its HTML).
	compiler *Compiler
	logger   *slog.Logger
}

// NewSavedBlockHandler builds the handler.
func NewSavedBlockHandler(repo *Repository, compiler *Compiler, logger *slog.Logger) *SavedBlockHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SavedBlockHandler{repo: repo, compiler: compiler, logger: logger}
}

// RegisterRoutes mounts the saved-block routes.
func (h *SavedBlockHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/saved-blocks")
	g.Use(protected...)
	g.Use(requireCap("marketing.manage"))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.PUT("/:id", h.Rename)
		// Synced-block operations: how many emails would an update touch, and
		// the update itself (which rewrites AND recompiles every dependent).
		g.GET("/:id/usage", h.Usage)
		g.PUT("/:id/content", h.UpdateContent)
		g.DELETE("/:id", h.Remove)
	}
}

type savedBlockRequest struct {
	Name   string          `json:"name"`
	Block  json.RawMessage `json:"block"`
	Synced bool            `json:"synced"`
}

// List returns the org's saved blocks.
func (h *SavedBlockHandler) List(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rows, err := h.repo.ListSavedBlocksByOrg(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not list saved blocks")
		return
	}
	if rows == nil {
		rows = []SavedBlock{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

// Create validates the block against the wire Block shape (parse + re-marshal,
// same doctrine as content save: unknown fields are dropped, never stored) and
// stores it.
func (h *SavedBlockHandler) Create(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	if n, err := h.repo.CountSavedBlocksByOrg(c.Request.Context(), orgID); err == nil && n >= maxSavedBlocksPerOrg {
		abortErr(c, http.StatusUnprocessableEntity, "saved-block library is full — delete unused blocks first")
		return
	}
	var req savedBlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		abortErr(c, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 120 {
		name = name[:120]
	}
	var blk Block
	if err := json.Unmarshal(req.Block, &blk); err != nil || strings.TrimSpace(blk.Type) == "" {
		abortErr(c, http.StatusBadRequest, "invalid block")
		return
	}
	clean, err := json.Marshal(blk)
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid block")
		return
	}
	row := &SavedBlock{OrgID: orgID, Name: name, Block: clean, Synced: req.Synced, CreatedBy: &userID}
	if err := h.repo.CreateSavedBlock(c.Request.Context(), row); err != nil {
		h.logger.Error("marketing: saved-block create failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not save the block")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": row})
}

// Rename updates a saved block's name (name-only — the block content itself is
// immutable; re-save from the builder to change it).
func (h *SavedBlockHandler) Rename(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid saved-block id")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		abortErr(c, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 120 {
		name = name[:120]
	}
	okUpd, err := h.repo.RenameSavedBlockByID(c.Request.Context(), orgID, id, name)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not rename the saved block")
		return
	}
	if !okUpd {
		abortErr(c, http.StatusNotFound, "saved block not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"renamed": true}})
}

// Remove deletes a saved block. Content already using inserted COPIES is
// unaffected — inserts are deep copies, not references.
func (h *SavedBlockHandler) Remove(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid saved-block id")
		return
	}
	okDel, err := h.repo.DeleteSavedBlockByID(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not delete the saved block")
		return
	}
	if !okDel {
		abortErr(c, http.StatusNotFound, "saved block not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}

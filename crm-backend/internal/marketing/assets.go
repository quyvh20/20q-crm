package marketing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MarketingAsset is one uploaded image for the email builder's image library
// (Klaviyo-style). Bytes live in Postgres — recipients' mail clients fetch them
// through the PUBLIC serve route, so storage must be durable and shared across
// backend instances (prod has no shared disk).
type MarketingAsset struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID       uuid.UUID  `gorm:"type:uuid;not null;index" json:"org_id"`
	Filename    string     `gorm:"type:varchar(255);not null" json:"filename"`
	// Folder is a flat organizational label ("" = unfiled) — same doctrine as
	// template folders: folders exist implicitly as the distinct values.
	Folder      string     `gorm:"type:varchar(80);not null;default:''" json:"folder"`
	ContentType string     `gorm:"type:varchar(100);not null" json:"content_type"`
	SizeBytes   int        `gorm:"type:int;not null" json:"size_bytes"`
	Data        []byte     `gorm:"type:bytea;not null" json:"-"`
	CreatedBy   *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt   time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

// TableName pins the table name.
func (MarketingAsset) TableName() string { return "marketing_assets" }

const (
	maxAssetBytes  = 2 << 20 // 2 MB per image — email images should be far smaller anyway
	maxAssetsPerOrg = 500
)

// assetContentTypes is the servable allowlist. The stored type is derived from
// content SNIFFING (http.DetectContentType), never the client's claim.
var assetContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// ── Repository ───────────────────────────────────────────────────────────────

// CreateAsset stores an uploaded image.
func (r *Repository) CreateAsset(ctx context.Context, a *MarketingAsset) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListAssetsByOrg returns the org's assets WITHOUT their bytes (grid metadata).
func (r *Repository) ListAssetsByOrg(ctx context.Context, orgID uuid.UUID) ([]MarketingAsset, error) {
	var rows []MarketingAsset
	err := r.db.WithContext(ctx).
		Select("id", "org_id", "filename", "folder", "content_type", "size_bytes", "created_by", "created_at").
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

// UpdateAssetFolder moves an asset between folders (column-only write).
func (r *Repository) UpdateAssetFolder(ctx context.Context, orgID, id uuid.UUID, folder string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&MarketingAsset{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Update("folder", folder)
	return res.RowsAffected > 0, res.Error
}

// CountAssetsByOrg supports the per-org cap.
func (r *Repository) CountAssetsByOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&MarketingAsset{}).Where("org_id = ?", orgID).Count(&n).Error
	return n, err
}

// GetAssetByID loads one asset WITH bytes. Deliberately NOT org-scoped: the
// serve route is public (recipients' mail clients carry no session) and the
// unguessable uuid is the capability, exactly like unsubscribe tokens.
func (r *Repository) GetAssetByID(ctx context.Context, id uuid.UUID) (*MarketingAsset, error) {
	var a MarketingAsset
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&a).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// DeleteAssetByID removes an org's asset (org-scoped — deletion is management).
func (r *Repository) DeleteAssetByID(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&MarketingAsset{})
	return res.RowsAffected > 0, res.Error
}

// ── Authed management handler ────────────────────────────────────────────────

// AssetHandler serves the image library: list/upload/delete under
// marketing.manage. publicBase is the base URL recipients can reach (prod: the
// first-party domain whose /api is proxied) used to mint each asset's public URL.
type AssetHandler struct {
	repo       *Repository
	publicBase string
	logger     *slog.Logger
}

// NewAssetHandler builds the handler.
func NewAssetHandler(repo *Repository, publicBase string, logger *slog.Logger) *AssetHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AssetHandler{repo: repo, publicBase: strings.TrimRight(publicBase, "/"), logger: logger}
}

// RegisterRoutes mounts the authed asset-library routes.
func (h *AssetHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/assets")
	g.Use(protected...)
	g.Use(requireCap("marketing.manage"))
	{
		g.GET("", h.List)
		g.POST("", h.Upload)
		g.POST("/:id/duplicate", h.Duplicate)
		g.PUT("/:id/folder", h.SetFolder)
		g.DELETE("/:id", h.Remove)
	}
}

// assetURL mints the public URL recipients (and the canvas) load the image from.
func (h *AssetHandler) assetURL(id uuid.UUID) string {
	return fmt.Sprintf("%s/api/marketing/asset/%s", h.publicBase, id.String())
}

func (h *AssetHandler) toJSON(a MarketingAsset) gin.H {
	return gin.H{
		"id":           a.ID,
		"filename":     a.Filename,
		"folder":       a.Folder,
		"content_type": a.ContentType,
		"size_bytes":   a.SizeBytes,
		"created_at":   a.CreatedAt,
		"url":          h.assetURL(a.ID),
	}
}

// SetFolder moves an asset between folders.
func (h *AssetHandler) SetFolder(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid image id")
		return
	}
	var req struct {
		Folder string `json:"folder"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	okUpd, err := h.repo.UpdateAssetFolder(c.Request.Context(), orgID, id, cleanFolder(req.Folder))
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not move the image")
		return
	}
	if !okUpd {
		abortErr(c, http.StatusNotFound, "image not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"moved": true}})
}

// List returns the org's image library (newest first).
func (h *AssetHandler) List(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rows, err := h.repo.ListAssetsByOrg(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not list images")
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		out = append(out, h.toJSON(a))
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// Upload accepts one multipart image, sniffs + allowlists its real content type,
// and stores it.
func (h *AssetHandler) Upload(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	if n, err := h.repo.CountAssetsByOrg(c.Request.Context(), orgID); err == nil && n >= maxAssetsPerOrg {
		abortErr(c, http.StatusUnprocessableEntity, "image library is full — delete unused images first")
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		abortErr(c, http.StatusBadRequest, "file required")
		return
	}
	if fileHeader.Size > maxAssetBytes {
		abortErr(c, http.StatusRequestEntityTooLarge, "images must be under 2 MB (email clients punish heavy images)")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		abortErr(c, http.StatusBadRequest, "cannot open file")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAssetBytes+1))
	if err != nil || len(data) == 0 {
		abortErr(c, http.StatusBadRequest, "cannot read file")
		return
	}
	if len(data) > maxAssetBytes {
		abortErr(c, http.StatusRequestEntityTooLarge, "images must be under 2 MB")
		return
	}
	ctype := sniffImageType(data)
	if ctype == "" {
		abortErr(c, http.StatusUnsupportedMediaType, "only PNG, JPEG, GIF or WebP images are supported")
		return
	}
	name := strings.TrimSpace(fileHeader.Filename)
	if name == "" {
		name = "image"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	a := &MarketingAsset{
		OrgID:       orgID,
		Filename:    name,
		Folder:      cleanFolder(c.PostForm("folder")), // uploads land in the active folder
		ContentType: ctype,
		SizeBytes:   len(data),
		Data:        data,
		CreatedBy:   &userID,
	}
	if err := h.repo.CreateAsset(c.Request.Context(), a); err != nil {
		h.logger.Error("marketing: asset upload failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not store the image")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": h.toJSON(*a)})
}

// copyFilename derives the duplicate's name, keeping the extension readable:
// "logo.png" → "logo (copy).png"; "readme" → "readme (copy)".
func copyFilename(name string) string {
	base, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		base, ext = name[:i], name[i:]
	}
	out := base + " (copy)" + ext
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

// Duplicate copies an asset server-side (the bytes never round-trip through the
// browser). The copy keeps the folder and gets its own uuid — sent emails
// referencing the original are unaffected.
func (h *AssetHandler) Duplicate(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid image id")
		return
	}
	if n, err := h.repo.CountAssetsByOrg(c.Request.Context(), orgID); err == nil && n >= maxAssetsPerOrg {
		abortErr(c, http.StatusUnprocessableEntity, "image library is full — delete unused images first")
		return
	}
	src, err := h.repo.GetAssetByID(c.Request.Context(), id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not load the image")
		return
	}
	// GetAssetByID is unscoped (it also serves the public route) — enforce org
	// ownership here, indistinguishable from absent.
	if src == nil || src.OrgID != orgID {
		abortErr(c, http.StatusNotFound, "image not found")
		return
	}
	dup := &MarketingAsset{
		OrgID:       orgID,
		Filename:    copyFilename(src.Filename),
		Folder:      src.Folder,
		ContentType: src.ContentType,
		SizeBytes:   src.SizeBytes,
		Data:        src.Data,
		CreatedBy:   &userID,
	}
	if err := h.repo.CreateAsset(c.Request.Context(), dup); err != nil {
		h.logger.Error("marketing: asset duplicate failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not duplicate the image")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": h.toJSON(*dup)})
}

// Remove deletes an asset. Emails already sent keep referencing the URL — it
// will 404 for recipients, so the UI warns before deleting.
func (h *AssetHandler) Remove(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid image id")
		return
	}
	okDel, err := h.repo.DeleteAssetByID(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not delete the image")
		return
	}
	if !okDel {
		abortErr(c, http.StatusNotFound, "image not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}

// sniffImageType returns the allowlisted content type detected from the BYTES,
// or "" when the content is not a supported image (the client's claimed type is
// never trusted).
func sniffImageType(data []byte) string {
	ct := http.DetectContentType(data)
	if assetContentTypes[ct] {
		return ct
	}
	return ""
}

// ── Public serve handler ─────────────────────────────────────────────────────

// PublicAssetHandler serves image bytes to ANYONE with the uuid — recipients'
// mail clients have no session. Mounted on the bare router like the unsubscribe
// endpoints; ip-limited; immutable-cached (assets are content-addressed by row).
type PublicAssetHandler struct {
	repo    *Repository
	limiter ipRateLimiter // nil-tolerant
}

// NewPublicAssetHandler builds the handler.
func NewPublicAssetHandler(repo *Repository, limiter ipRateLimiter) *PublicAssetHandler {
	return &PublicAssetHandler{repo: repo, limiter: limiter}
}

// RegisterRoutes mounts the public route. `asset` (singular) is a static
// segment distinct from the authed /api/marketing/assets group, mirroring the
// /u/:token doctrine so the param can't collide with management routes.
func (h *PublicAssetHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/api/marketing/asset/:id", h.ipGuard, h.Serve)
}

func (h *PublicAssetHandler) ipGuard(c *gin.Context) {
	if h.limiter != nil {
		if ok, _ := h.limiter.AllowN(c.Request.Context(), "mktasset:ip:"+c.ClientIP(), 1); !ok {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
	}
	c.Next()
}

// Serve writes the image bytes with long-lived caching.
func (h *PublicAssetHandler) Serve(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	a, err := h.repo.GetAssetByID(c.Request.Context(), id)
	if err != nil || a == nil || !assetContentTypes[a.ContentType] {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, a.ContentType, a.Data)
}

package http

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type VoiceHandler struct {
	uc domain.VoiceNoteUseCase
}

func NewVoiceHandler(uc domain.VoiceNoteUseCase) *VoiceHandler {
	return &VoiceHandler{uc: uc}
}

func (h *VoiceHandler) Upload(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userUUID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}

	// 25 MB, down from 500 MB. The whole payload is read into memory below, so the
	// old ceiling let a handful of concurrent uploads exhaust the process — and no
	// voice memo is 500 MB. Roughly 4 hours of 16 kbps speech.
	const maxSize = 25 << 20
	if fileHeader.Size > maxSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "audio file must be under 25 MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot open file"})
		return
	}
	defer file.Close()

	// LimitReader as well as the Size check: fileHeader.Size comes from the request,
	// so it is a claim, not a fact. Read one byte past the cap to tell "exactly at
	// the limit" from "lied about the size".
	audioBytes, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot read file"})
		return
	}
	if len(audioBytes) > maxSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "audio file must be under 25 MB"})
		return
	}

	languageCode := c.DefaultPostForm("language_code", "en")
	durationSec := 0
	if d := c.PostForm("duration_seconds"); d != "" {
		durationSec, _ = strconv.Atoi(d)
	}

	input := domain.UploadVoiceNoteInput{
		AudioBytes:      audioBytes,
		OriginalName:    fileHeader.Filename,
		LanguageCode:    languageCode,
		DurationSeconds: durationSec,
		AutoAnalyze:     c.PostForm("analyze") != "false", // Default to true for backward compatibility
	}

	if cid := c.PostForm("contact_id"); cid != "" {
		if parsed, err := uuid.Parse(cid); err == nil {
			input.ContactID = &parsed
		}
	}
	if did := c.PostForm("deal_id"); did != "" {
		if parsed, err := uuid.Parse(did); err == nil {
			input.DealID = &parsed
		}
	}

	// The auth middleware already put the caller's FULL scope on the context (scope +
	// user + ROLE). Re-wrapping it here with WithDataScope overwrote the role with
	// uuid.Nil, so a record shared to the caller's ROLE stopped matching on this path.
	ctx := c.Request.Context()

	note, jobID, err := h.uc.Upload(ctx, orgUUID, userUUID, input)
	if err != nil {
		// The usecase returns a 415 AppError when the bytes are not audio. Blanket
		// 500-with-err.Error() turned that into "internal error" plus whatever the
		// wrapped message said — the user could not tell a rejected file from a
		// broken server, and internal paths leaked into the response body.
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			c.JSON(appErr.Code, gin.H{"error": appErr.Message})
			return
		}
		log.Printf("voice upload failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save the recording"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data": gin.H{
			"voice_note": note,
			"job_id":     jobID,
		},
	})
}

func (h *VoiceHandler) List(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var f domain.VoiceNoteFilter
	if cid := c.Query("contact_id"); cid != "" {
		if parsed, err := uuid.Parse(cid); err == nil {
			f.ContactID = &parsed
		}
	}
	if did := c.Query("deal_id"); did != "" {
		if parsed, err := uuid.Parse(did); err == nil {
			f.DealID = &parsed
		}
	}
	if l := c.Query("limit"); l != "" {
		f.Limit, _ = strconv.Atoi(l)
	}

	// The auth middleware already put the caller's FULL scope on the context (scope +
	// user + ROLE). Re-wrapping it here with WithDataScope overwrote the role with
	// uuid.Nil, so a record shared to the caller's ROLE stopped matching on this path.
	ctx := c.Request.Context()

	notes, err := h.uc.List(ctx, orgUUID, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": notes})
}

func (h *VoiceHandler) GetByID(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// The auth middleware already put the caller's FULL scope on the context (scope +
	// user + ROLE). Re-wrapping it here with WithDataScope overwrote the role with
	// uuid.Nil, so a record shared to the caller's ROLE stopped matching on this path.
	ctx := c.Request.Context()

	note, err := h.uc.GetByID(ctx, orgUUID, noteID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "voice note not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": note})
}

func (h *VoiceHandler) ApplyUpdates(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// The auth middleware already put the caller's FULL scope on the context (scope +
	// user + ROLE). Re-wrapping it here with WithDataScope overwrote the role with
	// uuid.Nil, so a record shared to the caller's ROLE stopped matching on this path.
	ctx := c.Request.Context()

	if err := h.uc.ApplyContactUpdates(ctx, orgUUID, noteID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "contact profile updated successfully"})
}

func (h *VoiceHandler) Delete(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// The auth middleware already put the caller's FULL scope on the context (scope +
	// user + ROLE). Re-wrapping it here with WithDataScope overwrote the role with
	// uuid.Nil, so a record shared to the caller's ROLE stopped matching on this path.
	ctx := c.Request.Context()

	if err := h.uc.Delete(ctx, orgUUID, noteID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "voice note deleted"})
}

func (h *VoiceHandler) Analyze(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userUUID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	noteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid voice note id"})
		return
	}

	// The auth middleware already put the caller's FULL scope on the context (scope +
	// user + ROLE). Re-wrapping it here with WithDataScope overwrote the role with
	// uuid.Nil, so a record shared to the caller's ROLE stopped matching on this path.
	ctx := c.Request.Context()

	err = h.uc.Analyze(ctx, orgUUID, userUUID, noteID)
	if err != nil {
		if strings.HasPrefix(err.Error(), "note is not in an analyzable state") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "analysis queued"})
}

// PreviewVoiceNote streams a stored recording.
//
// Two things here are load-bearing and must not be "simplified" away.
//
// ORG SCOPE: the filename alone used to be the whole authorization. Any
// authenticated user of ANY org could fetch any recording by naming its file, so
// knowing (or guessing) a filename crossed the tenant boundary. The name is now
// resolved back to a voice note the CALLER'S org owns before a byte is served.
//
// CONTENT TYPE: served from our own extension mapping with nosniff, never from
// net/http's extension lookup. Prod proxies /api through a Cloudflare Pages
// Function, so this response is same-origin with the SPA — a file served as
// text/html here executes with the session cookies in reach. Legacy rows still
// carry uploader-chosen extensions, so this cannot be relaxed for them either.
func (h *VoiceHandler) PreviewVoiceNote(c *gin.Context) {
	orgUUID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	filename := c.Param("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename required"})
		return
	}
	cleanFileName := filepath.Base(filename)

	// The stored file_url is the authorization record: it exists on exactly one
	// voice note, and that note belongs to exactly one org.
	if !h.uc.OwnsStoredFile(c.Request.Context(), orgUUID, "/api/voice/preview/"+cleanFileName) {
		// 404, not 403 — a 403 would confirm the recording exists to a caller in
		// another org.
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	filePath := filepath.Join("storage", "voice_notes", cleanFileName)
	f, err := os.Open(filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", usecase.ContentTypeForStoredVoiceFile(cleanFileName))
	// ServeContent (not c.File) so the type we set is not overwritten by net/http's
	// extension sniffing, while still getting Range support for the audio player.
	http.ServeContent(c.Writer, c.Request, cleanFileName, info.ModTime(), f)
}

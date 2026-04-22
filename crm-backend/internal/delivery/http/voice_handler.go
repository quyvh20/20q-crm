package http

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/internal/repository"

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

	const maxSize = 500 << 20 // 500 MB
	if fileHeader.Size > maxSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "audio file must be under 500 MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot open file"})
		return
	}
	defer file.Close()

	audioBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot read file"})
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

	role, _ := GetRole(c)
	ctx := repository.WithDataScope(c.Request.Context(), role, userUUID)

	note, jobID, err := h.uc.Upload(ctx, orgUUID, userUUID, input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	userUUID, _ := GetUserID(c)
	role, _ := GetRole(c)
	ctx := repository.WithDataScope(c.Request.Context(), role, userUUID)

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

	userUUID, _ := GetUserID(c)
	role, _ := GetRole(c)
	ctx := repository.WithDataScope(c.Request.Context(), role, userUUID)

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

	userUUID, _ := GetUserID(c)
	role, _ := GetRole(c)
	ctx := repository.WithDataScope(c.Request.Context(), role, userUUID)

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

	userUUID, _ := GetUserID(c)
	role, _ := GetRole(c)
	ctx := repository.WithDataScope(c.Request.Context(), role, userUUID)

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

	role, _ := GetRole(c)
	ctx := repository.WithDataScope(c.Request.Context(), role, userUUID)

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

func (h *VoiceHandler) PreviewVoiceNote(c *gin.Context) {
	filename := c.Param("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename required"})
		return
	}

	// Prevent directory traversal
	cleanFileName := filepath.Base(filename)
	filePath := filepath.Join("storage", "voice_notes", cleanFileName)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	c.File(filePath)
}

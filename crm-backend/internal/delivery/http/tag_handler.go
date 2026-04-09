package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TagHandler struct {
	tagUC domain.TagUseCase
}

func NewTagHandler(uc domain.TagUseCase) *TagHandler {
	return &TagHandler{tagUC: uc}
}

// GET /api/tags
func (h *TagHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	tags, err := h.tagUC.List(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(tags))
}

// GET /api/tags/:id
func (h *TagHandler) GetByID(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid tag id"))
		return
	}

	tag, err := h.tagUC.GetByID(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(tag))
}

// POST /api/tags
func (h *TagHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.CreateTagInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	tag, err := h.tagUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusCreated, domain.Success(tag))
}

// PUT /api/tags/:id
func (h *TagHandler) Update(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid tag id"))
		return
	}

	var input domain.UpdateTagInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	tag, err := h.tagUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(tag))
}

// DELETE /api/tags/:id
func (h *TagHandler) Delete(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid tag id"))
		return
	}

	if err := h.tagUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "tag deleted"}))
}

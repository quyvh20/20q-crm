package http

import (
	"net/http"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ContactMergeHandler is R8.3's duplicate-detection + merge surface.
type ContactMergeHandler struct {
	uc usecase.ContactMergeUseCase
}

func NewContactMergeHandler(uc usecase.ContactMergeUseCase) *ContactMergeHandler {
	return &ContactMergeHandler{uc: uc}
}

// ListDuplicates handles GET /api/contacts/duplicates
func (h *ContactMergeHandler) ListDuplicates(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	groups, err := h.uc.ListDuplicates(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(groups))
}

type mergeContactsRequest struct {
	SurvivorID uuid.UUID `json:"survivor_id" binding:"required"`
	LoserID    uuid.UUID `json:"loser_id" binding:"required"`
}

// Merge handles POST /api/contacts/merge
func (h *ContactMergeHandler) Merge(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, _ := c.Get("user_id")
	actorID, _ := userID.(uuid.UUID)

	var req mergeContactsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	survivor, err := h.uc.Merge(c.Request.Context(), orgID, actorID, req.SurvivorID, req.LoserID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(survivor))
}

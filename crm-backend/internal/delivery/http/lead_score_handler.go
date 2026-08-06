package http

import (
	"errors"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LeadScoringHandler serves the R9.4 scoring-rules admin surface.
type LeadScoringHandler struct {
	uc domain.LeadScoringUseCase
}

func NewLeadScoringHandler(uc domain.LeadScoringUseCase) *LeadScoringHandler {
	return &LeadScoringHandler{uc: uc}
}

// GET /api/lead-scoring/rules
func (h *LeadScoringHandler) ListRules(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	rules, err := h.uc.ListRules(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(rules))
}

// POST /api/lead-scoring/rules
func (h *LeadScoringHandler) CreateRule(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var in domain.CreateLeadScoringRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	rule, err := h.uc.CreateRule(c.Request.Context(), orgID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, domain.Success(rule))
}

// PUT /api/lead-scoring/rules/:id
func (h *LeadScoringHandler) UpdateRule(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid rule id"))
		return
	}
	var in domain.UpdateLeadScoringRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	rule, err := h.uc.UpdateRule(c.Request.Context(), orgID, id, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(rule))
}

// DELETE /api/lead-scoring/rules/:id
func (h *LeadScoringHandler) DeleteRule(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid rule id"))
		return
	}
	if err := h.uc.DeleteRule(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"deleted": true}))
}

// POST /api/lead-scoring/recompute
//
// Synchronous, unlike the fire-and-forget rescore after a rule edit: this is an
// explicit "apply now" and the caller is waiting to see the number move. The
// per-org round cap bounds how long it can take.
func (h *LeadScoringHandler) Recompute(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	n, err := h.uc.RecomputeOrgNow(c.Request.Context(), orgID)
	if err != nil {
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			c.JSON(appErr.Code, domain.Err(appErr.Message))
			return
		}
		// A truncated pass still wrote rows — report what landed rather than
		// implying nothing happened.
		c.JSON(http.StatusOK, domain.Success(gin.H{"contacts": n, "complete": false}))
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"contacts": n, "complete": true}))
}

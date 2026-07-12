package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CompanyHandler struct {
	companyUC domain.CompanyUseCase
	masker    fieldMasker
}

func NewCompanyHandler(uc domain.CompanyUseCase) *CompanyHandler {
	return &CompanyHandler{companyUC: uc}
}

// SetFieldMasker wires Field-Level Security onto the legacy company routes
// (called from RegisterRoutes; nil in unit tests → empty mask).
func (h *CompanyHandler) SetFieldMasker(m fieldMasker) { h.masker = m }

// GET /api/companies
func (h *CompanyHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var f domain.CompanyFilter
	if err := c.ShouldBindQuery(&f); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	companies, nextCursor, err := h.companyUC.List(c.Request.Context(), orgID, f)
	if err != nil {
		handleAppError(c, err)
		return
	}

	count, _ := h.companyUC.Count(c.Request.Context(), orgID)
	mask := legacyMask(h.masker, c.Request.Context(), orgID, "company")
	c.JSON(http.StatusOK, domain.SuccessWithMeta(maskLegacy(mask, "company", companies), domain.CursorMeta{
		NextCursor: nextCursor,
		HasMore:    nextCursor != "",
		Total:      count,
	}))
}

// GET /api/companies/:id
func (h *CompanyHandler) GetByID(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid company id"))
		return
	}

	company, err := h.companyUC.GetByID(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}

	mask := legacyMask(h.masker, c.Request.Context(), orgID, "company")
	c.JSON(http.StatusOK, domain.Success(maskLegacy(mask, "company", company)))
}

// POST /api/companies
func (h *CompanyHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.CreateCompanyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	mask := legacyMask(h.masker, c.Request.Context(), orgID, "company")
	if err := guardLegacyWrite(mask, companyCreateKeys(input)); err != nil {
		handleAppError(c, err)
		return
	}

	company, err := h.companyUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusCreated, domain.Success(maskLegacy(mask, "company", company)))
}

// PUT /api/companies/:id
func (h *CompanyHandler) Update(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid company id"))
		return
	}

	var input domain.UpdateCompanyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	mask := legacyMask(h.masker, c.Request.Context(), orgID, "company")
	if err := guardLegacyWrite(mask, companyUpdateKeys(input)); err != nil {
		handleAppError(c, err)
		return
	}

	company, err := h.companyUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(maskLegacy(mask, "company", company)))
}

// DELETE /api/companies/:id
func (h *CompanyHandler) Delete(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid company id"))
		return
	}

	if err := h.companyUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "company deleted"}))
}

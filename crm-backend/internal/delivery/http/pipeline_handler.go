package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SchemaInvalidator is a callback to invalidate the workflow schema cache
// when stages, tags, custom fields, or custom objects change.
type SchemaInvalidator func(orgID uuid.UUID)

type PipelineHandler struct {
	stageUC          domain.PipelineStageUseCase
	pipelineUC       domain.PipelineUseCase
	invalidateSchema SchemaInvalidator
}

func NewPipelineHandler(stageUC domain.PipelineStageUseCase, pipelineUC domain.PipelineUseCase) *PipelineHandler {
	return &PipelineHandler{stageUC: stageUC, pipelineUC: pipelineUC}
}

// optionalPipelineID reads ?pipeline_id=. Absent (or unparseable) yields nil,
// which every layer below reads as "every stage in the org" — the exact
// pre-R9.3 behaviour, so callers that resolve stage NAMES for display keep
// working untouched. A caller rendering a BOARD must pass one.
func optionalPipelineID(c *gin.Context) *uuid.UUID {
	raw := c.Query("pipeline_id")
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// SetSchemaInvalidator sets the callback to invalidate the workflow schema cache.
func (h *PipelineHandler) SetSchemaInvalidator(fn SchemaInvalidator) {
	h.invalidateSchema = fn
}

func (h *PipelineHandler) invalidateSchemaIfSet(orgID uuid.UUID) {
	if h.invalidateSchema != nil {
		h.invalidateSchema(orgID)
	}
}

// GET /api/pipeline/stages
func (h *PipelineHandler) ListStages(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	stages, err := h.stageUC.List(c.Request.Context(), orgID, optionalPipelineID(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(stages))
}

// POST /api/pipeline/stages
func (h *PipelineHandler) CreateStage(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var input domain.CreateStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	stage, err := h.stageUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusCreated, domain.Success(stage))
}

// PUT /api/pipeline/stages/:id
func (h *PipelineHandler) UpdateStage(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid stage id"))
		return
	}
	var input domain.UpdateStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	stage, err := h.stageUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusOK, domain.Success(stage))
}

// DELETE /api/pipeline/stages/:id
func (h *PipelineHandler) DeleteStage(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid stage id"))
		return
	}
	if err := h.stageUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusOK, domain.Success(gin.H{"deleted": true}))
}

// POST /api/pipeline/stages/seed-defaults
func (h *PipelineHandler) SeedDefaultStages(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	stages, err := h.stageUC.SeedDefaults(c.Request.Context(), orgID, optionalPipelineID(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusOK, domain.Success(stages))
}

// ============================================================
// Pipelines (R9.3)
// ============================================================

// GET /api/pipeline/pipelines
func (h *PipelineHandler) ListPipelines(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	out, err := h.pipelineUC.List(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(out))
}

// POST /api/pipeline/pipelines
func (h *PipelineHandler) CreatePipeline(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var input domain.CreatePipelineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	p, err := h.pipelineUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	// A new pipeline can carry stages (seed_default_stages), and the workflow
	// schema caches an org's stage list for 60s — without this the builder
	// serves a stale ladder that omits the new board entirely.
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusCreated, domain.Success(p))
}

// PUT /api/pipeline/pipelines/:id
func (h *PipelineHandler) UpdatePipeline(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid pipeline id"))
		return
	}
	var input domain.UpdatePipelineInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	p, err := h.pipelineUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusOK, domain.Success(p))
}

// DELETE /api/pipeline/pipelines/:id
func (h *PipelineHandler) DeletePipeline(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid pipeline id"))
		return
	}
	if err := h.pipelineUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID)
	c.JSON(http.StatusOK, domain.Success(gin.H{"deleted": true}))
}

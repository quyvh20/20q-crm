package http

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReportHandler serves report definitions and runs (P9). Definition visibility
// and management gating live in the usecase; the only route-level gate is
// data.export on the CSV endpoint (matching the audit export). Report DATA is
// re-authorized per viewer inside the usecase (OLS → FLS → data scope), so
// these handlers only translate HTTP.
type ReportHandler struct {
	uc domain.ReportUseCase
}

func NewReportHandler(uc domain.ReportUseCase) *ReportHandler {
	return &ReportHandler{uc: uc}
}

func (h *ReportHandler) callerIDs(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	orgID, okOrg := GetOrgID(c)
	userID, okUser := GetUserID(c)
	if !okOrg || !okUser {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func reportIDParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid report id"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *ReportHandler) List(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	reports, err := h.uc.List(c.Request.Context(), orgID, userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": reports, "error": nil})
}

func (h *ReportHandler) Create(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var in domain.ReportInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	rep, err := h.uc.Create(c.Request.Context(), orgID, userID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": rep, "error": nil})
}

func (h *ReportHandler) Get(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := reportIDParam(c)
	if !ok {
		return
	}
	rep, err := h.uc.Get(c.Request.Context(), orgID, userID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rep, "error": nil})
}

func (h *ReportHandler) Update(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := reportIDParam(c)
	if !ok {
		return
	}
	var in domain.ReportInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	rep, err := h.uc.Update(c.Request.Context(), orgID, userID, id, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rep, "error": nil})
}

func (h *ReportHandler) Delete(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := reportIDParam(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, userID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}, "error": nil})
}

func (h *ReportHandler) Run(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := reportIDParam(c)
	if !ok {
		return
	}
	res, err := h.uc.Run(c.Request.Context(), orgID, userID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res, "error": nil})
}

// previewRequest runs an unsaved config — the builder's live preview.
type previewRequest struct {
	ObjectSlug string              `json:"object_slug" binding:"required"`
	Config     domain.ReportConfig `json:"config"`
}

func (h *ReportHandler) Preview(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var in previewRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	res, err := h.uc.Preview(c.Request.Context(), orgID, in.ObjectSlug, in.Config)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res, "error": nil})
}

// ListFields returns the object's queryable field catalog (registry + virtual
// fields, minus the caller's FLS-hidden ones) — the builder UI's field list.
func (h *ReportHandler) ListFields(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	fields, err := h.uc.ListFields(c.Request.Context(), orgID, c.Param("slug"))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": fields, "error": nil})
}

// ExportCSV streams one report run as a CSV attachment (route-gated on
// data.export). Shape follows the result kind: table rows export their
// columns; grouped charts export label/value/count; a KPI exports one row.
func (h *ReportHandler) ExportCSV(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := reportIDParam(c)
	if !ok {
		return
	}
	rep, err := h.uc.Get(c.Request.Context(), orgID, userID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	res, err := h.uc.Run(c.Request.Context(), orgID, userID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+reportCSVFilename(rep.Name)+`"`)

	w := csv.NewWriter(c.Writer)
	switch res.Kind {
	case domain.ReportResultRows:
		_ = w.Write(res.Columns)
		for _, row := range res.Rows {
			record := make([]string, 0, len(res.Columns))
			for _, col := range res.Columns {
				record = append(record, csvSafe(reportCSVCell(row[col])))
			}
			_ = w.Write(record)
		}
	case domain.ReportResultGroups:
		_ = w.Write([]string{"label", "value", "count"})
		for _, g := range res.Groups {
			_ = w.Write([]string{
				csvSafe(g.Label),
				strconv.FormatFloat(g.Value, 'f', -1, 64),
				strconv.Itoa(g.Count),
			})
		}
	default: // scalar
		_ = w.Write([]string{"value", "row_count"})
		_ = w.Write([]string{strconv.FormatFloat(res.Value, 'f', -1, 64), strconv.Itoa(res.RowCount)})
	}
	w.Flush()
}

// reportCSVFilename derives a safe attachment name from the report's title.
func reportCSVFilename(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "report"
	}
	return base + ".csv"
}

func reportCSVCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprintf("%v", v)
}

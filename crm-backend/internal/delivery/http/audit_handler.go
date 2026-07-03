package http

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AuditHandler serves the admin/auth audit log (P4). Both routes are gated on the
// audit.view capability at the router; the handler re-reads org from context so a
// caller only ever sees their own org's events.
type AuditHandler struct {
	uc domain.AuditUseCase
}

func NewAuditHandler(uc domain.AuditUseCase) *AuditHandler {
	return &AuditHandler{uc: uc}
}

// parseAuditFilter builds the query filter from ?category=&type=&actor=&from=&to=
// &limit=&offset=. from/to are RFC3339 timestamps; unparseable values are ignored
// (treated as "no filter") rather than erroring, so a malformed query degrades to
// a broader result instead of a 400.
func parseAuditFilter(c *gin.Context) domain.AuthEventFilter {
	f := domain.AuthEventFilter{
		Category:  c.Query("category"),
		EventType: c.Query("type"),
	}
	if a := c.Query("actor"); a != "" {
		if id, err := uuid.Parse(a); err == nil {
			f.ActorID = &id
		}
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = &t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = &t
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}
	return f
}

func (h *AuditHandler) ListEvents(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	events, total, err := h.uc.ListEvents(c.Request.Context(), orgID, parseAuditFilter(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": events, "total": total, "error": nil})
}

// ExportCSV streams the filtered audit log as a CSV attachment. It applies the
// same filters as ListEvents (from offset 0) so the export matches the on-screen
// view, capped at the usecase's export limit.
func (h *AuditHandler) ExportCSV(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	f := parseAuditFilter(c)
	f.Offset = 0
	f.Limit = 10000
	events, _, err := h.uc.ListEvents(c.Request.Context(), orgID, f)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="audit-log.csv"`)

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"timestamp", "category", "event_type", "actor", "actor_email", "target_id", "ip", "user_agent", "metadata"})
	for _, e := range events {
		_ = w.Write([]string{
			e.CreatedAt.UTC().Format(time.RFC3339),
			csvSafe(e.Category),
			csvSafe(e.EventType),
			csvSafe(e.ActorName),
			csvSafe(e.ActorEmail),
			derefUUID(e.TargetID),
			csvSafe(derefStr(e.IP)),
			csvSafe(derefStr(e.UserAgent)),
			csvSafe(string(e.Metadata)),
		})
	}
	w.Flush()
}

// csvSafe neutralizes CSV/spreadsheet formula injection (CWE-1236). Several audit
// fields are attacker-controlled — a login's User-Agent header and a user's
// free-form full name land in auth_events verbatim. If such a value opens with
// =, +, -, @ (or a tab/CR that a leading formula char can hide behind), Excel and
// LibreOffice evaluate the cell as a formula when the admin opens the export.
// Prefixing with a single quote forces the cell to be treated as text.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefUUID(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

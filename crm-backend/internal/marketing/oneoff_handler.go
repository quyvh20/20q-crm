package marketing

import (
	"errors"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OneToOneEmailHandler exposes R9's "1:1 email from a record" over HTTP.
type OneToOneEmailHandler struct {
	uc *OneToOneEmailUseCase
}

func NewOneToOneEmailHandler(uc *OneToOneEmailUseCase) *OneToOneEmailHandler {
	return &OneToOneEmailHandler{uc: uc}
}

type sendOneToOneRequest struct {
	Slug     string `json:"slug" binding:"required"`
	RecordID string `json:"record_id" binding:"required"`
	Subject  string `json:"subject" binding:"required"`
	BodyHTML string `json:"body_html" binding:"required"`
}

// RegisterRoutes mounts on the authed stack — every caller needs an org and a
// user identity (the sender). No capability gate: 1:1 email is a per-record
// action available to anyone who could otherwise see the record, not a
// marketing.manage-gated bulk surface.
func (h *OneToOneEmailHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc) {
	g := router.Group("/api/email")
	g.Use(protected...)
	g.POST("/send", h.Send)
}

// Send handles POST /api/email/send.
func (h *OneToOneEmailHandler) Send(c *gin.Context) {
	orgIDVal, ok := c.Get("org_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := orgIDVal.(uuid.UUID)
	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(uuid.UUID)

	var req sendOneToOneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	recordID, err := uuid.Parse(req.RecordID)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid record_id"))
		return
	}

	activity, err := h.uc.Send(c.Request.Context(), orgID, userID, req.Slug, recordID, req.Subject, req.BodyHTML)
	if err != nil {
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			c.JSON(appErr.Code, domain.Err(appErr.Message))
			return
		}
		c.JSON(http.StatusInternalServerError, domain.Err("failed to send email"))
		return
	}
	c.JSON(http.StatusOK, domain.Success(activity))
}

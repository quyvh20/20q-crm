package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// UserGroupHandler serves user-group CRUD + membership. Mutations are gated at
// the router by groups.manage; GET is open to any member (the share picker).
type UserGroupHandler struct {
	uc domain.UserGroupUseCase
}

func NewUserGroupHandler(uc domain.UserGroupUseCase) *UserGroupHandler {
	return &UserGroupHandler{uc: uc}
}

func (h *UserGroupHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	groups, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": groups, "error": nil})
}

func (h *UserGroupHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	actorID, _ := GetUserID(c)
	var in domain.UserGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	g, err := h.uc.Create(c.Request.Context(), orgID, actorID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": g, "error": nil})
}

func (h *UserGroupHandler) Update(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid group id"))
		return
	}
	var in domain.UserGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	g, err := h.uc.Update(c.Request.Context(), orgID, id, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": g, "error": nil})
}

func (h *UserGroupHandler) Delete(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid group id"))
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}, "error": nil})
}

type groupMemberInput struct {
	UserID uuid.UUID `json:"user_id" binding:"required"`
}

func (h *UserGroupHandler) AddMember(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid group id"))
		return
	}
	var in groupMemberInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	if err := h.uc.AddMember(c.Request.Context(), orgID, groupID, in.UserID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"added": true}, "error": nil})
}

func (h *UserGroupHandler) RemoveMember(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid group id"))
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid user id"))
		return
	}
	if err := h.uc.RemoveMember(c.Request.Context(), orgID, groupID, userID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}, "error": nil})
}

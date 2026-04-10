package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
)

type UserHandler struct {
	userRepo domain.UserRepository
}

func NewUserHandler(userRepo domain.UserRepository) *UserHandler {
	return &UserHandler{userRepo: userRepo}
}

// GET /api/users — list all users in the current org (for assignee dropdowns)
func (h *UserHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	users, err := h.userRepo.ListByOrgID(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}

	// Return minimal user info for dropdown
	type UserListItem struct {
		ID        string `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Email     string `json:"email"`
	}

	var items []UserListItem
	for _, u := range users {
		items = append(items, UserListItem{
			ID:        u.ID.String(),
			FirstName: u.FirstName,
			LastName:  u.LastName,
			Email:     u.Email,
		})
	}
	c.JSON(http.StatusOK, domain.Success(items))
}

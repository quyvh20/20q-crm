package domain

import "net/http"

type APIResponse struct {
	Data  interface{} `json:"data"`
	Error *string     `json:"error"`
	Meta  interface{} `json:"meta,omitempty"`
}

type PaginationMeta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

func Success(data interface{}) APIResponse {
	return APIResponse{Data: data, Error: nil}
}

func SuccessWithMeta(data interface{}, meta interface{}) APIResponse {
	return APIResponse{Data: data, Error: nil, Meta: meta}
}

func Err(message string) APIResponse {
	return APIResponse{Data: nil, Error: &message}
}

type AppError struct {
	Code    int    `json:"-"`
	Message string `json:"message"`
}

func (e *AppError) Error() string {
	return e.Message
}

func NewAppError(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

var (
	ErrInvalidCredentials    = NewAppError(http.StatusUnauthorized, "invalid email or password")
	ErrEmailAlreadyExists    = NewAppError(http.StatusConflict, "email already registered")
	ErrUserNotFound          = NewAppError(http.StatusNotFound, "user not found")
	ErrInvalidToken          = NewAppError(http.StatusUnauthorized, "invalid or expired token")
	ErrTokenRevoked          = NewAppError(http.StatusUnauthorized, "token has been revoked")
	ErrTokenExpired          = NewAppError(http.StatusUnauthorized, "token has expired")
	ErrForbidden             = NewAppError(http.StatusForbidden, "insufficient permissions")
	ErrInternal              = NewAppError(http.StatusInternalServerError, "internal server error")
	ErrContactNotFound       = NewAppError(http.StatusNotFound, "contact not found")
	ErrDealNotFound          = NewAppError(http.StatusNotFound, "deal not found")
	ErrStageNotFound         = NewAppError(http.StatusNotFound, "pipeline stage not found")
	ErrInvalidFile           = NewAppError(http.StatusBadRequest, "invalid file format, expected CSV or XLSX")
	ErrOrgNotFound           = NewAppError(http.StatusNotFound, "organization not found")
	ErrNotMember             = NewAppError(http.StatusForbidden, "you are not a member of this workspace")
	ErrAlreadyMember         = NewAppError(http.StatusConflict, "user is already a member of this workspace")
	ErrCannotRemoveSuperAdmin = NewAppError(http.StatusForbidden, "cannot remove or demote the workspace creator")
)

type CursorMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
	Total      int64  `json:"total,omitempty"`
}

package domain

import "net/http"

// APIResponse is the standard JSON envelope for all API responses.
type APIResponse struct {
	Data  interface{} `json:"data"`
	Error *string     `json:"error"`
	Meta  interface{} `json:"meta,omitempty"`
}

// PaginationMeta holds pagination info for list endpoints.
type PaginationMeta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// Success returns a standard success response.
func Success(data interface{}) APIResponse {
	return APIResponse{Data: data, Error: nil}
}

// SuccessWithMeta returns a standard success response with metadata.
func SuccessWithMeta(data interface{}, meta interface{}) APIResponse {
	return APIResponse{Data: data, Error: nil, Meta: meta}
}

// Err returns a standard error response.
func Err(message string) APIResponse {
	return APIResponse{Data: nil, Error: &message}
}

// ============================================================
// Application Errors
// ============================================================

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
	ErrInvalidCredentials = NewAppError(http.StatusUnauthorized, "invalid email or password")
	ErrEmailAlreadyExists = NewAppError(http.StatusConflict, "email already registered")
	ErrUserNotFound       = NewAppError(http.StatusNotFound, "user not found")
	ErrInvalidToken       = NewAppError(http.StatusUnauthorized, "invalid or expired token")
	ErrTokenRevoked       = NewAppError(http.StatusUnauthorized, "token has been revoked")
	ErrTokenExpired       = NewAppError(http.StatusUnauthorized, "token has expired")
	ErrForbidden          = NewAppError(http.StatusForbidden, "insufficient permissions")
	ErrInternal           = NewAppError(http.StatusInternalServerError, "internal server error")
	ErrContactNotFound    = NewAppError(http.StatusNotFound, "contact not found")
	ErrDealNotFound       = NewAppError(http.StatusNotFound, "deal not found")
	ErrStageNotFound      = NewAppError(http.StatusNotFound, "pipeline stage not found")
	ErrInvalidFile        = NewAppError(http.StatusBadRequest, "invalid file format, expected CSV or XLSX")
)

// CursorMeta holds cursor-based pagination info.
type CursorMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
	Total      int64  `json:"total,omitempty"`
}


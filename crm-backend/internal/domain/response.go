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
	// RetryAfter, when > 0, is emitted as a Retry-After response header (seconds).
	// Used for rate-limit / lockout responses (P2). Construct a fresh AppError to
	// set it — never mutate a shared sentinel.
	RetryAfter int `json:"-"`
}

func (e *AppError) Error() string {
	return e.Message
}

func NewAppError(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

// OrgUnavailableError is returned by RefreshToken when the caller asks to refresh
// into a specific org they are no longer an ACTIVE member of (R2 fail-closed, P3).
// It carries the caller's current workspaces so the handler can answer 409
// {code:"ORG_UNAVAILABLE", workspaces:[...]} and the SPA can route to the chooser
// ("You no longer have access to Acme") instead of silently flipping the user
// into a different org.
type OrgUnavailableError struct {
	Workspaces []WorkspaceInfo
}

func (e *OrgUnavailableError) Error() string { return "workspace no longer available" }

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

	// Account recovery + verification (P1)
	ErrInvalidResetToken  = NewAppError(http.StatusBadRequest, "this password reset link is invalid or has expired")
	ErrInvalidVerifyToken = NewAppError(http.StatusBadRequest, "this verification link is invalid or has expired")
	ErrEmailNotVerified   = NewAppError(http.StatusForbidden, "please verify your email address before performing this action")
	ErrResendTooSoon      = NewAppError(http.StatusTooManyRequests, "please wait a moment before requesting another verification email")

	// Attack hardening (P2)
	ErrTooManyRequests   = NewAppError(http.StatusTooManyRequests, "too many requests, please slow down")
	ErrTooManyLoginAttempts = NewAppError(http.StatusTooManyRequests, "too many failed attempts, please try again later")
	ErrTokenReuse        = NewAppError(http.StatusUnauthorized, "session ended for security reasons, please sign in again")
	ErrMissingCSRFToken  = NewAppError(http.StatusForbidden, "missing or invalid CSRF token")
)

type CursorMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
	Total      int64  `json:"total,omitempty"`
}

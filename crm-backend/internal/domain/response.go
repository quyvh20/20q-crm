package domain

import (
	"fmt"
	"net/http"
)

type APIResponse struct {
	Data  interface{} `json:"data"`
	Error *string     `json:"error"`
	Meta  interface{} `json:"meta,omitempty"`
	// RequestID is the server's correlation id for a FAILED request (U7.4). The same
	// id is on the X-Request-ID response header and on the zap access-log line, so a
	// user who quotes it from the error banner leads straight to the log entry. It
	// rides in the body as well as the header because a header can be stripped by a
	// proxy on the way to the browser, and the one moment it matters is the one
	// moment the request already went wrong.
	RequestID string `json:"request_id,omitempty"`
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

// ErrWithRef is Err plus the request's correlation id, so the failure the user sees
// carries the same reference the server logged it under.
func ErrWithRef(message, requestID string) APIResponse {
	return APIResponse{Data: nil, Error: &message, RequestID: requestID}
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

// CodeReassignmentRequired is the machine-readable code the RemoveMember 409
// carries so the SPA opens the reassignment dialog off the code, never a
// message substring.
const CodeReassignmentRequired = "REASSIGNMENT_REQUIRED"

// ReassignmentRequiredError is returned by RemoveMember when the target still
// owns records and the caller supplied no strategy. The handler renders it as
// 409 {code:"REASSIGNMENT_REQUIRED", owned:{contacts,deals,custom}} so the admin
// decides with real counts in front of them (U0.2). Custom records joined the
// count in U6.3, when custom objects gained an owner.
type ReassignmentRequiredError struct {
	Contacts int64
	Deals    int64
	Custom   int64
}

func (e *ReassignmentRequiredError) Error() string {
	if e.Custom > 0 {
		return fmt.Sprintf("this member still owns %d contact(s), %d deal(s) and %d other record(s) — transfer them to another member or leave them unassigned", e.Contacts, e.Deals, e.Custom)
	}
	return fmt.Sprintf("this member still owns %d contact(s) and %d deal(s) — transfer them to another member or leave them unassigned", e.Contacts, e.Deals)
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
	// ErrContactEmailExists is the (org_id, email) unique-index violation surfaced
	// as itself instead of a blanket 500. Two callers need to tell it apart from a
	// genuine DB failure: the REST API (a re-submitted email is the caller's
	// mistake, 409, not ours) and lead ingestion, whose upsert loop recovers from
	// it by re-matching and updating instead of failing the lead. Note the index is
	// case-SENSITIVE and partial (email IS NOT NULL AND deleted_at IS NULL), so a
	// case-variant or soft-deleted twin does not raise this.
	ErrContactEmailExists = NewAppError(http.StatusConflict, "a contact with this email already exists")
	ErrDealNotFound          = NewAppError(http.StatusNotFound, "deal not found")
	ErrStageNotFound         = NewAppError(http.StatusNotFound, "pipeline stage not found")
	ErrInvalidFile           = NewAppError(http.StatusBadRequest, "invalid file format, expected CSV or XLSX")
	ErrOrgNotFound           = NewAppError(http.StatusNotFound, "organization not found")
	ErrNotMember             = NewAppError(http.StatusForbidden, "you are not a member of this workspace")
	// ErrRecordNotWritable: the record is visible to the caller only through a
	// view-level share, so writes are rejected (U0.4 — share levels are enforced,
	// not decorative).
	ErrRecordNotWritable     = NewAppError(http.StatusForbidden, "this record is shared with you as view-only — ask the owner for edit access")
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

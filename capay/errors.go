package capay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Sentinel errors for the HTTP statuses the documentation's status-code table
// lists. Match them with errors.Is:
//
//	if errors.Is(err, capay.ErrConflict) { … }
var (
	// ErrBadRequest is HTTP 400 — a malformed request or a validation failure.
	// The API is precise about what is wrong: APIError.Messages carries one
	// entry per failed rule ("amount must not be less than 1", "paymentDate
	// should use format YYYY-MM-DD"). Business rejections such as a duplicate
	// beneficiary arrive as 400 too.
	ErrBadRequest = errors.New("capay: bad request")

	// ErrUnauthorized is HTTP 401 — missing or expired access token. The
	// client refreshes the token and retries once before surfacing this.
	ErrUnauthorized = errors.New("capay: unauthorized")

	// ErrForbidden is HTTP 403 — the resource exists but these credentials
	// may not access it.
	ErrForbidden = errors.New("capay: forbidden")

	// ErrNotFound is HTTP 404 — no resource behind the URL. The API answers
	// an unknown or malformed id with the message "Invalid id".
	ErrNotFound = errors.New("capay: not found")

	// ErrConflict is HTTP 409 — a replayed x-idempotency-key. The first
	// request with that key already performed the action.
	ErrConflict = errors.New("capay: duplicate idempotency key")

	// ErrTooManyRequests is HTTP 429. Not in the documented table, but the
	// SDK treats it as retryable for reads.
	ErrTooManyRequests = errors.New("capay: too many requests")

	// ErrInternalServer is HTTP 500.
	ErrInternalServer = errors.New("capay: internal server error")

	// ErrServiceUnavailable is HTTP 503.
	ErrServiceUnavailable = errors.New("capay: service unavailable")

	// ErrGatewayTimeout is HTTP 504.
	ErrGatewayTimeout = errors.New("capay: gateway timeout")
)

// ErrInvalidInput is returned when a request is rejected locally, before any
// network call, because it cannot satisfy a rule the documentation states — a
// missing id, a beneficiary with neither bank account nor uniquePayId, an
// idempotency key longer than 36 characters.
//
// Catching these client-side keeps a malformed request from consuming a round
// trip and surfaces the reason as a Go error rather than a 400.
var ErrInvalidInput = errors.New("capay: invalid input")

// APIError is returned for any non-2xx response. It carries the HTTP status,
// the API's own message(s), the raw body and the endpoint that produced it,
// and wraps a sentinel so callers can branch with errors.Is without string
// matching.
//
// The API's error body has a fixed shape:
//
//	{"statusCode":400,"timestamp":"…","path":"/payments",
//	 "message":["amount must not be less than 1", …],"error":"Bad Request"}
//
// where message is a string on most failures and an array of strings on
// validation failures. Both are decoded: Message is always populated with a
// single human-readable line, and Messages holds every entry when there were
// several.
type APIError struct {
	// StatusCode is the HTTP status returned by the API.
	StatusCode int

	// Message is the human-readable reason. When the API returned several
	// (a validation failure), this is the first; see Messages.
	Message string

	// Messages holds every message the API returned, one per failed rule.
	// It has one element when Message was a plain string.
	Messages []string

	// ErrorText is the API's "error" field, a status phrase such as
	// "Bad Request". Empty on some responses.
	ErrorText string

	// Path is the request path the API echoed back.
	Path string

	// Timestamp is the API's own timestamp for the failure, as sent.
	Timestamp string

	// Body is the response body exactly as received, for the cases where
	// Message is not enough.
	Body string

	// Method and Endpoint identify the call that failed.
	Method   string
	Endpoint string

	// sentinel is the wrapped error that errors.Is matches against.
	sentinel error
}

// Error implements the error interface.
func (e *APIError) Error() string {
	msg := e.Message
	if len(e.Messages) > 1 {
		msg = strings.Join(e.Messages, "; ")
	}
	if msg == "" {
		msg = truncate(e.Body, 256)
	}
	return fmt.Sprintf("capay: %s %s failed with status %d: %s",
		e.Method, e.Endpoint, e.StatusCode, msg)
}

// Unwrap exposes the sentinel so errors.Is(err, ErrNotFound) works.
func (e *APIError) Unwrap() error { return e.sentinel }

// errorBody is the API's error envelope. Message is decoded through
// json.RawMessage because it is a string on most failures and an array on
// validation failures.
type errorBody struct {
	StatusCode int             `json:"statusCode"`
	Timestamp  string          `json:"timestamp"`
	Path       string          `json:"path"`
	Message    json.RawMessage `json:"message"`
	Error      string          `json:"error"`
}

// messages unpacks the message field into a slice whichever shape it took.
func (b errorBody) messages() []string {
	if len(b.Message) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(b.Message, &one); err == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(b.Message, &many); err == nil {
		return many
	}
	// Some other JSON value: keep its literal form rather than lose it.
	return []string{string(b.Message)}
}

// newAPIError builds an APIError from a failed response, mapping the status
// onto a sentinel. A body that is not the documented envelope (an HTML error
// page from a proxy, say) yields empty message fields and the raw Body.
func newAPIError(status int, body []byte, method, endpoint string) *APIError {
	e := &APIError{
		StatusCode: status,
		Body:       string(body),
		Method:     method,
		Endpoint:   endpoint,
		sentinel:   sentinelFor(status),
	}

	var eb errorBody
	if err := json.Unmarshal(body, &eb); err == nil {
		e.Messages = eb.messages()
		if len(e.Messages) > 0 {
			e.Message = e.Messages[0]
		}
		e.ErrorText = eb.Error
		e.Path = eb.Path
		e.Timestamp = eb.Timestamp
	}
	return e
}

// sentinelFor maps an HTTP status onto the documented sentinel error.
// Undocumented statuses fall through to a generic error so callers still get a
// usable errors.Is target for "some 4xx/5xx".
func sentinelFor(status int) error {
	switch status {
	case http.StatusBadRequest:
		return ErrBadRequest
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	case http.StatusTooManyRequests:
		return ErrTooManyRequests
	case http.StatusInternalServerError:
		return ErrInternalServer
	case http.StatusServiceUnavailable:
		return ErrServiceUnavailable
	case http.StatusGatewayTimeout:
		return ErrGatewayTimeout
	default:
		return errors.New("capay: unexpected response status")
	}
}

// retryableStatus reports whether a status is worth retrying.
//
// Only transient, server-side conditions qualify. Note that this says nothing
// about whether the *request* may be retried — see client.do, which
// additionally requires the method to be a read.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// invalidInput builds an ErrInvalidInput with a reason.
func invalidInput(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

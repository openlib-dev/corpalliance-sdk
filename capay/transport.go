package capay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	jsonContent = "application/json"

	// maxIdempotencyKeyLength is the documented cap on x-idempotency-key.
	maxIdempotencyKeyLength = 36

	// maxErrorBody bounds how much of a failed response is retained.
	maxBodyRead = 4 << 20
)

// RequestOption customises a single call. Pass them after the request struct:
//
//	c.Payments().Create(ctx, req, capay.WithIdempotencyKey(key))
type RequestOption func(*requestSpec)

// WithIdempotencyKey sets the x-idempotency-key header (≤ 36 characters).
//
// Every mutating endpoint accepts one. If the same key is replayed the API
// answers HTTP 409 (ErrConflict) instead of performing the action twice. Use
// it on every payment and deposit-affecting call so a network retry on your
// side can never double-act.
func WithIdempotencyKey(key string) RequestOption {
	return func(s *requestSpec) { s.idempotencyKey = key }
}

// requestSpec describes one API call. Service methods build a spec and hand it
// to client.do, which owns authentication, retries, error mapping and decoding
// so no individual endpoint has to.
type requestSpec struct {
	// api is the endpoint descriptor.
	api api

	// pathParams substitutes {placeholders} in the endpoint path. Values are
	// URL-escaped, so an id or referenceId is safe to pass raw.
	pathParams map[string]string

	// query holds query-string parameters.
	query url.Values

	// body is marshalled as JSON when non-nil.
	body any

	// result receives the decoded response, or nil to discard the body.
	result any

	// idempotencyKey is sent as x-idempotency-key when non-empty.
	idempotencyKey string
}

// apply runs the per-call options and validates what they set.
func (s *requestSpec) apply(opts []RequestOption) error {
	for _, o := range opts {
		o(s)
	}
	if len(s.idempotencyKey) > maxIdempotencyKeyLength {
		return invalidInput("idempotency key %q is longer than %d characters",
			s.idempotencyKey, maxIdempotencyKeyLength)
	}
	return nil
}

// baseFor returns the base URL the client should use.
func (c *client) baseFor() string {
	if c.baseURL != "" {
		return strings.TrimSuffix(c.baseURL, "/")
	}
	return baseURLFor(c.env)
}

// resolvePath returns the endpoint path with all {placeholders} substituted
// and escaped.
func resolvePath(a api, params map[string]string) string {
	path := a.Path
	for key, value := range params {
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(value))
	}
	return path
}

// do executes a request: authenticate, send, retry where safe, map errors,
// decode.
func (c *client) do(ctx context.Context, spec requestSpec) error {
	endpoint := resolvePath(spec.api, spec.pathParams)
	fullURL := c.baseFor() + endpoint
	if len(spec.query) > 0 {
		fullURL += "?" + spec.query.Encode()
	}

	// The body is marshalled once and replayed per attempt.
	var payload []byte
	if spec.body != nil {
		var err error
		payload, err = json.Marshal(spec.body)
		if err != nil {
			return fmt.Errorf("capay: encoding %s %s request: %w", spec.api.Method, endpoint, err)
		}
	}

	// Retries are restricted to reads. See RetryPolicy for why a mutating
	// request is never replayed even with an idempotency key.
	maxAttempts := 1
	if c.retry.MaxAttempts > 1 && spec.api.Method == http.MethodGet {
		maxAttempts = c.retry.MaxAttempts
	}

	// refreshed guards the 401 path so a persistently rejected credential
	// cannot loop: we force a new token at most once per call.
	refreshed := false

	for attempt := 1; ; attempt++ {
		token, err := c.ensureToken(ctx)
		if err != nil {
			return err
		}

		req, err := c.newRequest(ctx, spec.api.Method, fullURL, payload)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if spec.idempotencyKey != "" {
			req.Header.Set("x-idempotency-key", spec.idempotencyKey)
		}

		status, body, err := c.send(req)
		if err != nil {
			// Transport-level failure (dial, TLS, timeout). Retryable only for
			// reads, and never once the context is done.
			lastErr := fmt.Errorf("capay: %s %s: %w", spec.api.Method, endpoint, err)
			if attempt < maxAttempts && ctx.Err() == nil {
				if waitErr := backoff(ctx, c.retry, attempt); waitErr == nil {
					continue
				}
			}
			return lastErr
		}

		// A 401 means the API rejected the request before acting on it, so
		// replaying it is safe even for a payment. With a one-hour token this
		// path is reached in normal operation, not only on misconfiguration.
		if status == http.StatusUnauthorized && !refreshed {
			refreshed = true
			c.invalidateToken(token)
			continue
		}

		if status >= http.StatusBadRequest {
			apiErr := newAPIError(status, body, spec.api.Method, endpoint)
			if attempt < maxAttempts && retryableStatus(status) {
				if waitErr := backoff(ctx, c.retry, attempt); waitErr == nil {
					continue
				}
			}
			return apiErr
		}

		if spec.result == nil || len(bytes.TrimSpace(body)) == 0 {
			return nil
		}

		if err := json.Unmarshal(body, spec.result); err != nil {
			return fmt.Errorf("capay: decoding %s %s response: %w (body: %s)",
				spec.api.Method, endpoint, err, truncate(string(body), 512))
		}
		return nil
	}
}

// newRequest builds an http.Request with the headers every call carries.
func (c *client) newRequest(ctx context.Context, method, fullURL string, payload []byte) (*http.Request, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return nil, fmt.Errorf("capay: building %s %s: %w", method, fullURL, err)
	}
	req.Header.Set("Accept", jsonContent)
	req.Header.Set("User-Agent", c.userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", jsonContent)
	}
	return req, nil
}

// send performs one HTTP round trip and drains the body.
func (c *client) send(req *http.Request) (int, []byte, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, maxBodyRead))
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("reading response body: %w", err)
	}
	return res.StatusCode, body, nil
}

// backoff sleeps before the next attempt, or returns early if the context
// ends.
//
// The delay doubles per attempt and carries ±25% jitter so that a fleet of
// workers throttled together does not retry in lockstep.
func backoff(ctx context.Context, p RetryPolicy, attempt int) error {
	delay := p.BaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= p.MaxDelay {
			delay = p.MaxDelay
			break
		}
	}
	if delay > p.MaxDelay {
		delay = p.MaxDelay
	}

	jitter := 1 + (rand.Float64()-0.5)/2 // [0.75, 1.25)
	delay = time.Duration(float64(delay) * jitter)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncate shortens s for inclusion in an error message.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// queryBuilder accumulates optional query parameters, skipping zero values so
// filter structs can be used with only the fields that matter.
type queryBuilder struct{ v url.Values }

func newQuery() *queryBuilder { return &queryBuilder{v: url.Values{}} }

func (q *queryBuilder) str(key, value string) *queryBuilder {
	if value != "" {
		q.v.Set(key, value)
	}
	return q
}

func (q *queryBuilder) intp(key string, value int) *queryBuilder {
	if value > 0 {
		q.v.Set(key, fmt.Sprint(value))
	}
	return q
}

func (q *queryBuilder) date(key string, value Date) *queryBuilder {
	if !value.IsZero() {
		q.v.Set(key, value.String())
	}
	return q
}

func (q *queryBuilder) time(key string, value time.Time) *queryBuilder {
	if !value.IsZero() {
		q.v.Set(key, value.UTC().Format(timestampLayout))
	}
	return q
}

func (q *queryBuilder) page(p ListOptions) *queryBuilder {
	// skip=0 is the API default, but is sent explicitly when a limit is set
	// so a caller's intent is unambiguous in logs.
	if p.Skip > 0 || p.Limit > 0 {
		q.v.Set("skip", fmt.Sprint(max(p.Skip, 0)))
	}
	return q.intp("limit", p.Limit)
}

func (q *queryBuilder) values() url.Values { return q.v }

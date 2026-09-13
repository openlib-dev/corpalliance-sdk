package capay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// authRequest is the body of the /authenticate call.
type authRequest struct {
	APIUser string `json:"apiUser"`
	APIKey  string `json:"apiKey"`
}

// authResponse is the /authenticate response. The API answers with HTTP 201.
//
//	{"accessToken":"eyJhbGciOiJIUzI1NiIs…","expiresInSeconds":3600}
type authResponse struct {
	AccessToken      string `json:"accessToken"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
}

// tokenClaims are the JWT claims of interest on the access token.
//
// The token is an HS256 JWT. Its signature is not verified — this SDK is the
// token's bearer, not its audience — but its payload is read for two things:
// the exp claim, as a cross-check on expiresInSeconds, and companyId, which is
// the caller's own clientId.
type tokenClaims struct {
	// Exp is the expiry, in Unix seconds.
	Exp int64 `json:"exp"`

	// CompanyID is the clientId of the account these credentials act for.
	CompanyID string `json:"companyId"`

	// APIUser echoes the apiUser the token was issued to.
	APIUser string `json:"apiUser"`
}

// parseTokenClaims decodes the payload segment of a JWT without verifying it.
//
// A token the SDK cannot parse is not an error: the caller's request would
// still succeed with it. In that case ok is false and the caller falls back to
// expiresInSeconds or the configured lifetime.
func parseTokenClaims(token string) (tokenClaims, bool) {
	var claims tokenClaims

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, false
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, false
	}
	return claims, true
}

// ensureToken returns a valid bearer token, fetching one if the cache is empty
// or close to expiry.
func (c *client) ensureToken(ctx context.Context) (string, error) {
	// Fast path: a cached token with time left on it.
	c.mu.RLock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		tok := c.token
		c.mu.RUnlock()
		return tok, nil
	}
	c.mu.RUnlock()

	// Slow path: singleflight collapses a burst of concurrent callers into one
	// token request, so a cold start under load does not stampede the API.
	v, err, _ := c.authGroup.Do("token", func() (any, error) {
		// Re-check: a competing goroutine may have refreshed while we queued.
		c.mu.RLock()
		if c.token != "" && time.Now().Before(c.tokenExpiry) {
			tok := c.token
			c.mu.RUnlock()
			return tok, nil
		}
		c.mu.RUnlock()

		res, err := c.fetchToken(ctx)
		if err != nil {
			return "", err
		}

		now := time.Now()
		token := res.AccessToken
		expiry := now.Add(c.tokenLifetime)
		clientID := ""

		// Precedence: the expiresInSeconds the API states, then the JWT's own
		// exp claim, then the configured fallback. The first two agree in
		// practice (exp - iat == expiresInSeconds); the claim is kept as a
		// backstop should the field ever go missing.
		claims, ok := parseTokenClaims(token)
		if ok {
			clientID = claims.CompanyID
		}
		switch {
		case res.ExpiresInSeconds > 0:
			expiry = now.Add(time.Duration(res.ExpiresInSeconds) * time.Second)
		case ok && claims.Exp > 0:
			expiry = time.Unix(claims.Exp, 0)
		}

		c.mu.Lock()
		c.token = token
		c.clientID = clientID
		c.tokenExpiry = expiry.Add(-c.refreshMargin)
		c.mu.Unlock()

		return token, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// invalidateToken clears the cached token, but only if it still matches the
// one the caller actually used.
//
// The guard matters under concurrency: if goroutine A receives a 401 for an
// old token while goroutine B has already installed a fresh one, A must not
// discard B's valid token and trigger a pointless refresh.
func (c *client) invalidateToken(used string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == used {
		c.token = ""
		c.tokenExpiry = time.Time{}
	}
}

// fetchToken performs the /authenticate call.
//
// This bypasses client.do deliberately: it is the one endpoint that carries no
// Authorization header, and it must not recurse into the token logic that
// wraps every other request.
func (c *client) fetchToken(ctx context.Context) (authResponse, error) {
	var out authResponse

	payload, err := json.Marshal(authRequest{APIUser: c.apiUser, APIKey: c.apiKey})
	if err != nil {
		return out, fmt.Errorf("capay: encoding authenticate request: %w", err)
	}

	req, err := c.newRequest(ctx, apiAuthenticate.Method, c.baseFor()+apiAuthenticate.Path, payload)
	if err != nil {
		return out, err
	}

	status, body, err := c.send(req)
	if err != nil {
		return out, fmt.Errorf("capay: authenticate request failed: %w", err)
	}

	if status >= http.StatusBadRequest {
		// Bad credentials arrive as HTTP 400 with a validation-style message
		// ("apiKey must be longer than or equal to 64 characters") or 401,
		// depending on which check fails first; the message is the useful
		// part either way.
		return out, newAPIError(status, body, apiAuthenticate.Method, apiAuthenticate.Path)
	}

	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("capay: decoding authenticate response: %w (body: %s)",
			err, truncate(string(body), 512))
	}

	if out.AccessToken == "" {
		// A 2xx with no token means the credentials were accepted but the API
		// returned something we do not understand. Failing loudly here is far
		// better than sending "Bearer " on every call.
		return out, fmt.Errorf("capay: authenticate response contained no accessToken (status %d): %s",
			status, truncate(string(body), 512))
	}

	return out, nil
}

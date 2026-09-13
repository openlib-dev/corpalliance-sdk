package capay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testJWT builds an unsigned-but-well-formed JWT carrying the given claims, so
// tests can exercise the same claim-reading path the real API drives.
func testJWT(t *testing.T, exp time.Time, companyID string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"exp":       exp.Unix(),
		"companyId": companyID,
		"apiUser":   "test-user",
	})
	if err != nil {
		t.Fatalf("marshalling claims: %v", err)
	}
	seg := base64.RawURLEncoding.EncodeToString(payload)
	return "header." + seg + ".signature"
}

// newTestClient wires a client at an httptest server with retries disabled,
// the shape almost every test wants.
func newTestClient(t *testing.T, handler http.HandlerFunc, opts ...Option) (Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	opts = append([]Option{WithBaseURL(srv.URL), WithoutRetry()}, opts...)
	c := New("test-user", "test-key", Sandbox, opts...)
	t.Cleanup(func() { _ = c.Close() })
	return c, srv
}

// tokenHandler serves an authenticate response with a 201, as the live API
// does, delegating everything else to next.
func tokenHandler(t *testing.T, token string, expiresIn int64, next http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiAuthenticate.Path {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(authResponse{AccessToken: token, ExpiresInSeconds: expiresIn})
			return
		}
		next(w, r)
	}
}

// writeJSON is a test-side helper for canned responses.
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

const balancesBody = `{"meta":{"totalCount":1,"skip":0,"limit":10,"timestamp":"2026-09-12T06:39:01.103Z"},
"data":[{"id":"01e3e810","currencyCode":"AUD","balance":1000000,"clientId":"e0874c5d","createdTime":"2026-09-11T05:59:48.000Z"}]}`

func TestAuthenticateSendsJSONCredentials(t *testing.T) {
	var gotType string
	var gotBody authRequest
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiAuthenticate.Path {
			gotType = r.Header.Get("Content-Type")
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			if r.Header.Get("Authorization") != "" {
				t.Error("authenticate must not carry an Authorization header")
			}
			writeJSON(w, http.StatusCreated, `{"accessToken":"tok","expiresInSeconds":3600}`)
			return
		}
		writeJSON(w, http.StatusOK, balancesBody)
	})

	if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
		t.Fatalf("Balances: %v", err)
	}
	if gotType != jsonContent {
		t.Errorf("Content-Type = %q, want %q", gotType, jsonContent)
	}
	if gotBody.APIUser != "test-user" || gotBody.APIKey != "test-key" {
		t.Errorf("credentials = %+v", gotBody)
	}
}

func TestTokenExpiryPrefersExpiresInSeconds(t *testing.T) {
	// expiresInSeconds says 10 minutes; the JWT claim says 1 hour. The API's
	// stated value wins.
	token := testJWT(t, time.Now().Add(time.Hour), "co-1")
	c, _ := newTestClient(t, tokenHandler(t, token, 600, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, balancesBody)
	}), WithTokenRefreshMargin(0))

	if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
		t.Fatal(err)
	}
	cl := c.(*client)
	remaining := time.Until(cl.tokenExpiry)
	if remaining < 9*time.Minute || remaining > 10*time.Minute+time.Second {
		t.Errorf("token expiry %v from now, want ~10m", remaining)
	}
}

func TestTokenExpiryFallsBackToJWTClaim(t *testing.T) {
	token := testJWT(t, time.Now().Add(20*time.Minute), "co-1")
	c, _ := newTestClient(t, tokenHandler(t, token, 0, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, balancesBody)
	}), WithTokenRefreshMargin(0))

	if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(c.(*client).tokenExpiry)
	if remaining < 19*time.Minute || remaining > 20*time.Minute+time.Second {
		t.Errorf("token expiry %v from now, want ~20m", remaining)
	}
}

func TestOpaqueTokenFallsBackToConfiguredLifetime(t *testing.T) {
	c, _ := newTestClient(t, tokenHandler(t, "opaque", 0, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, balancesBody)
	}), WithTokenLifetime(7*time.Minute), WithTokenRefreshMargin(0))

	if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(c.(*client).tokenExpiry)
	if remaining < 6*time.Minute || remaining > 7*time.Minute+time.Second {
		t.Errorf("token expiry %v from now, want ~7m", remaining)
	}
}

func TestClientIDComesFromCompanyIDClaim(t *testing.T) {
	token := testJWT(t, time.Now().Add(time.Hour), "e0874c5d-b2fa")
	c, _ := newTestClient(t, tokenHandler(t, token, 3600, nil))

	id, err := c.ClientID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != "e0874c5d-b2fa" {
		t.Errorf("ClientID = %q", id)
	}
}

func TestTokenIsCachedAcrossCalls(t *testing.T) {
	var authCalls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiAuthenticate.Path {
			authCalls.Add(1)
			writeJSON(w, http.StatusCreated, `{"accessToken":"tok","expiresInSeconds":3600}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		writeJSON(w, http.StatusOK, balancesBody)
	})

	for range 5 {
		if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := authCalls.Load(); n != 1 {
		t.Errorf("authenticate called %d times, want 1", n)
	}
}

func TestConcurrentCallsShareOneTokenFetch(t *testing.T) {
	var authCalls atomic.Int32
	release := make(chan struct{})
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiAuthenticate.Path {
			authCalls.Add(1)
			<-release
			writeJSON(w, http.StatusCreated, `{"accessToken":"tok","expiresInSeconds":3600}`)
			return
		}
		writeJSON(w, http.StatusOK, balancesBody)
	})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := authCalls.Load(); n != 1 {
		t.Errorf("authenticate called %d times under concurrency, want 1", n)
	}
}

func TestUnauthorizedTriggersOneRefreshAndRetry(t *testing.T) {
	var authCalls, dataCalls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiAuthenticate.Path {
			n := authCalls.Add(1)
			writeJSON(w, http.StatusCreated, `{"accessToken":"tok`+string(rune('0'+n))+`","expiresInSeconds":3600}`)
			return
		}
		if dataCalls.Add(1) == 1 {
			writeJSON(w, http.StatusUnauthorized, `{"statusCode":401,"message":"Missing or invalid authorization header"}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok2" {
			t.Errorf("retry used %q, want the refreshed token", got)
		}
		writeJSON(w, http.StatusOK, balancesBody)
	})

	if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
		t.Fatalf("expected the 401 to be absorbed, got %v", err)
	}
	if authCalls.Load() != 2 || dataCalls.Load() != 2 {
		t.Errorf("auth=%d data=%d, want 2/2", authCalls.Load(), dataCalls.Load())
	}
}

func TestPersistentUnauthorizedDoesNotLoop(t *testing.T) {
	var dataCalls atomic.Int32
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		dataCalls.Add(1)
		writeJSON(w, http.StatusUnauthorized, `{"statusCode":401,"message":"nope"}`)
	}))

	_, err := c.Accounts().Balances(context.Background(), BalanceFilter{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if n := dataCalls.Load(); n != 2 {
		t.Errorf("data endpoint hit %d times, want exactly 2 (original + one retry)", n)
	}
}

func TestBadCredentialsSurfaceAPIMessage(t *testing.T) {
	// Observed live: a short apiKey is rejected as a validation error with a
	// 400 and an array-valued message, before the credential is checked.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"statusCode":400,"timestamp":"2026-09-12T06:39:17.123Z","path":"/authenticate","message":["apiKey must be longer than or equal to 64 characters"],"error":"Bad Request"}`)
	})

	_, err := c.ClientID(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %v, want *APIError", err, err)
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("errors.Is(ErrBadRequest) = false")
	}
	if apiErr.Message != "apiKey must be longer than or equal to 64 characters" {
		t.Errorf("Message = %q", apiErr.Message)
	}
	if apiErr.Path != "/authenticate" || apiErr.ErrorText != "Bad Request" {
		t.Errorf("Path/ErrorText = %q/%q", apiErr.Path, apiErr.ErrorText)
	}
}

func TestAuthenticateWithoutTokenFailsLoudly(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, `{"expiresInSeconds":3600}`)
	})
	_, err := c.ClientID(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no accessToken") {
		t.Fatalf("err = %v, want a missing-token error", err)
	}
}

func TestValidationErrorCarriesEveryMessage(t *testing.T) {
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"statusCode":400,"timestamp":"t","path":"/payments","message":["beneficiaryId must be a UUID","amount must not be less than 1"],"error":"Bad Request"}`)
	}))

	_, err := c.Payments().Get(context.Background(), "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if len(apiErr.Messages) != 2 || apiErr.Message != "beneficiaryId must be a UUID" {
		t.Errorf("Messages = %q, Message = %q", apiErr.Messages, apiErr.Message)
	}
	if !strings.Contains(err.Error(), "amount must not be less than 1") {
		t.Errorf("Error() should join every message: %s", err)
	}
}

func TestStatusSentinels(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{400, ErrBadRequest}, {401, ErrUnauthorized}, {403, ErrForbidden}, {404, ErrNotFound},
		{409, ErrConflict}, {429, ErrTooManyRequests}, {500, ErrInternalServer},
		{503, ErrServiceUnavailable}, {504, ErrGatewayTimeout},
	}
	for _, tc := range cases {
		err := newAPIError(tc.status, []byte(`{"statusCode":0,"message":"m"}`), "GET", "/x")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: errors.Is(%v) = false", tc.status, tc.want)
		}
	}
	// An undocumented status must not masquerade as a documented one.
	if errors.Is(newAPIError(418, []byte("teapot"), "GET", "/x"), ErrBadRequest) {
		t.Errorf("418 must not map onto ErrBadRequest")
	}
}

func TestNonJSONErrorBodyIsPreserved(t *testing.T) {
	err := newAPIError(502, []byte("<html>bad gateway</html>"), "GET", "/x")
	if err.Message != "" || err.Body != "<html>bad gateway</html>" {
		t.Errorf("Message=%q Body=%q", err.Message, err.Body)
	}
	if !strings.Contains(err.Error(), "bad gateway") {
		t.Errorf("Error() = %s", err)
	}
}

func TestReadsAreRetriedOnTransientStatus(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			writeJSON(w, http.StatusServiceUnavailable, `{"statusCode":503,"message":"down"}`)
			return
		}
		writeJSON(w, http.StatusOK, balancesBody)
	}), WithRetry(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}))

	if _, err := c.Accounts().Balances(context.Background(), BalanceFilter{}); err != nil {
		t.Fatalf("expected the retries to succeed, got %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestMutationsAreNeverRetried(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusServiceUnavailable, `{"statusCode":503,"message":"down"}`)
	}), WithRetry(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}))

	_, err := c.Payments().Create(context.Background(), validPayment(), WithIdempotencyKey("k1"))
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("a payment was sent %d times, want exactly 1", calls.Load())
	}
}

func TestIdempotencyKeyIsSentAndBounded(t *testing.T) {
	var got string
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-idempotency-key")
		writeJSON(w, http.StatusCreated, `{"id":"p1","status":"SCHEDULED"}`)
	}))

	if _, err := c.Payments().Create(context.Background(), validPayment(), WithIdempotencyKey("order-42")); err != nil {
		t.Fatal(err)
	}
	if got != "order-42" {
		t.Errorf("x-idempotency-key = %q", got)
	}

	long := strings.Repeat("k", 37)
	_, err := c.Payments().Create(context.Background(), validPayment(), WithIdempotencyKey(long))
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a 37-character key must be rejected locally, got %v", err)
	}
}

func TestConflictMapsToErrConflict(t *testing.T) {
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusConflict, `{"statusCode":409,"message":"Duplicate idempotency key detected"}`)
	}))
	_, err := c.Payments().Create(context.Background(), validPayment(), WithIdempotencyKey("k"))
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestPathParamsAreEscaped(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		writeJSON(w, http.StatusOK, `{"id":"x"}`)
	}))
	if _, err := c.Payments().Get(context.Background(), "ref/with space"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/payments/ref%2Fwith%20space" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusServiceUnavailable, `{"statusCode":503}`)
	}), WithRetry(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Second}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Accounts().Balances(ctx, BalanceFilter{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want the backoff to be interrupted after 1", calls.Load())
	}
}

// validPayment is a request that passes local validation.
func validPayment() CreatePaymentRequest {
	return CreatePaymentRequest{
		BeneficiaryID:    "b1",
		CurrencyCode:     "AUD",
		Amount:           10,
		ChargeType:       ChargeShared,
		PaymentReference: "Invoice-1",
		PaymentDate:      NewDate(2026, 9, 30),
		PurposeCode:      PurposeGoods,
	}
}

// Package capay is an unofficial Go SDK for the Corporate Alliance CAPAY Client
// API — the REST/JSON API that lets a Corporate Alliance client's own software
// hold balances across currencies, send payments, collect deposits through
// virtual accounts, lock FX quotations and receive webhook notifications.
//
// It implements the CAPAY Client API specification v1.0.0 published at
// https://developer.corporatealliance.com/client/intro, verified against the
// sandbox at api.sandbox.capay.com.au.
//
// # Getting started
//
//	c := capay.New("api-user", "api-key", capay.Sandbox)
//	defer c.Close()
//
//	balances, err := c.Accounts().Balances(ctx, capay.BalanceFilter{})
//	if err != nil {
//		return err
//	}
//
// # Services
//
//	c.Accounts()        // balances, virtual accounts
//	c.Transactions()    // the ledger
//	c.Beneficiaries()   // business and personal payees, validation, Confirmation of Payee
//	c.Customers()       // the underlying customers you act for (the API's /clients)
//	c.Payments()        // payments, FX quotations
//	c.Deposits()        // inbound deposits, sandbox simulation
//	c.Rates()           // indicative daily rates
//	c.Webhooks()        // subscriptions; see also DecryptWebhook
//
// # clientId
//
// One idea shapes almost every call: the optional clientId. Omit it and the
// call acts on your own institutional account; supply the clientId of one of
// your underlying customers and the same call acts for that customer — its
// beneficiaries, its Sub-Account, its deposits. Every request and filter struct
// that accepts it exposes a ClientID field for exactly this purpose. Your own
// clientId is available from Client.ClientID.
//
// # Idempotency
//
// Every mutating endpoint accepts an x-idempotency-key header. Pass one with
// WithIdempotencyKey on any create, update, delete or cancel call; a replayed
// key is answered with HTTP 409 (ErrConflict) rather than a second action.
// Payment and deposit creation should always carry one.
//
// # Precision
//
// Amounts are float64, matching the JSON numbers the API sends. This SDK never
// performs arithmetic on money; the API accepts at most two decimal places.
package capay

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// defaultTokenLifetime is used only when the token response carries no
	// expiresInSeconds and the token is not a readable JWT. The documented
	// lifetime is 60 minutes.
	defaultTokenLifetime = 60 * time.Minute

	// defaultRefreshMargin is subtracted from the lifetime so a token is
	// replaced slightly before the API would reject it, absorbing clock skew
	// and in-flight latency.
	defaultRefreshMargin = 60 * time.Second

	// defaultTimeout bounds a single HTTP attempt.
	defaultTimeout = 60 * time.Second
)

// Client is the CAPAY SDK surface. Obtain one with New.
type Client interface {
	// Accounts exposes balances and virtual accounts.
	Accounts() AccountsService

	// Transactions exposes the ledger.
	Transactions() TransactionsService

	// Beneficiaries exposes business and personal payees.
	Beneficiaries() BeneficiariesService

	// Customers exposes the underlying customers you act for. The API calls
	// these "clients" (/clients/business, /clients/personal); the guides call
	// them Customers to distinguish them from you, the Client.
	Customers() CustomersService

	// Payments exposes payment creation, lifecycle and FX quotations.
	Payments() PaymentsService

	// Deposits exposes inbound deposits and the sandbox simulator.
	Deposits() DepositsService

	// Rates exposes the indicative daily rate.
	Rates() RatesService

	// Webhooks exposes webhook subscriptions.
	Webhooks() WebhooksService

	// ClientID returns your own clientId — the value the API associates with
	// these credentials — fetching a token first if none is cached.
	//
	// It is read from the companyId claim of the access token and matches the
	// clientId field on every record you own (balances, virtual accounts,
	// deposits). Useful for asserting at start-up that a deployment is pointed
	// at the account its operator thinks it is.
	ClientID(ctx context.Context) (string, error)

	// Close releases idle connections held by the underlying transport.
	Close() error
}

// RetryPolicy governs automatic retries.
//
// Retries apply only to read requests (HTTP GET). A mutating request is never
// retried by the SDK even when it carries an idempotency key: a replay after a
// timeout would be answered with 409 if the first attempt landed, and the SDK
// cannot tell that 409 apart from a genuine duplicate. Callers who want
// at-most-once semantics should resend with the same WithIdempotencyKey and
// treat ErrConflict as "already done".
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts including the first.
	// A value below 2 disables retries.
	MaxAttempts int

	// BaseDelay is the first backoff interval; it doubles per attempt and is
	// jittered by up to ±25% to avoid synchronised retries across workers.
	BaseDelay time.Duration

	// MaxDelay caps the backoff interval.
	MaxDelay time.Duration
}

// DefaultRetryPolicy retries twice after the initial attempt.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts: 3,
	BaseDelay:   500 * time.Millisecond,
	MaxDelay:    5 * time.Second,
}

// client is the unexported implementation behind Client.
type client struct {
	apiUser string
	apiKey  string
	env     Environment

	// baseURL overrides the environment default when non-empty. This is the
	// seam that makes the whole surface testable against one httptest server.
	baseURL string

	http      *http.Client
	userAgent string
	retry     RetryPolicy

	tokenLifetime time.Duration
	refreshMargin time.Duration

	// mu guards the cached token below.
	mu          sync.RWMutex
	token       string
	tokenExpiry time.Time
	clientID    string

	// authGroup collapses concurrent token refreshes into a single request.
	authGroup singleflight.Group
}

// Option customises a client at construction.
type Option func(*client)

// WithHTTPClient supplies a pre-configured http.Client — useful for custom
// TLS, proxies or instrumentation. The SDK still sets per-request headers and
// auth.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *client) {
		if hc != nil {
			c.http = hc
		}
	}
}

// WithTimeout sets the per-attempt HTTP timeout. Default 60s.
func WithTimeout(d time.Duration) Option {
	return func(c *client) {
		if d > 0 {
			c.http.Timeout = d
		}
	}
}

// WithBaseURL points every endpoint at url instead of the environment default.
func WithBaseURL(url string) Option {
	return func(c *client) { c.baseURL = url }
}

// WithUserAgent replaces the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// WithTokenLifetime overrides the assumed access-token lifetime.
//
// This is a fallback, not an override: the SDK prefers the expiresInSeconds the
// token endpoint returns, then the exp claim of the JWT, and only then this
// value.
func WithTokenLifetime(d time.Duration) Option {
	return func(c *client) {
		if d > 0 {
			c.tokenLifetime = d
		}
	}
}

// WithTokenRefreshMargin sets how long before expiry a token is replaced.
// Default 60s.
func WithTokenRefreshMargin(d time.Duration) Option {
	return func(c *client) {
		if d >= 0 {
			c.refreshMargin = d
		}
	}
}

// WithRetry replaces the retry policy. Retries still apply only to reads.
func WithRetry(p RetryPolicy) Option {
	return func(c *client) { c.retry = p }
}

// WithoutRetry disables automatic retries entirely.
func WithoutRetry() Option {
	return func(c *client) { c.retry = RetryPolicy{MaxAttempts: 1} }
}

// New builds a CAPAY client.
//
// apiUser and apiKey are the credentials Corporate Alliance's Integration
// Support team issues per environment. The apiKey is at least 64 characters
// long — the API rejects anything shorter before checking it — and grants full
// access to the account, so treat it like a password.
//
// New performs no network I/O: the first token is fetched lazily on the first
// call, so construction cannot fail and never blocks.
func New(apiUser, apiKey string, env Environment, options ...Option) Client {
	c := &client{
		apiUser:       apiUser,
		apiKey:        apiKey,
		env:           env,
		retry:         DefaultRetryPolicy,
		tokenLifetime: defaultTokenLifetime,
		refreshMargin: defaultRefreshMargin,
		userAgent:     "corpalliance-sdk-go/" + Version,
		http:          &http.Client{Transport: newTransport(), Timeout: defaultTimeout},
	}

	for _, opt := range options {
		opt(c)
	}

	return c
}

// Version is the SDK version reported in the User-Agent header.
const Version = "0.1.0"

// newTransport builds an http.Transport with defaults suited to a long-lived
// API client: bounded pools, a TLS 1.2 floor, and explicit timeouts at each
// stage so a stalled endpoint cannot pin a connection indefinitely.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		MaxConnsPerHost:       20,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// Service accessors. Each returns a thin value sharing the parent client's
// token cache and transport, so every group authenticates once.

func (c *client) Accounts() AccountsService           { return &accountsService{c: c} }
func (c *client) Transactions() TransactionsService   { return &transactionsService{c: c} }
func (c *client) Beneficiaries() BeneficiariesService { return &beneficiariesService{c: c} }
func (c *client) Customers() CustomersService         { return &customersService{c: c} }
func (c *client) Payments() PaymentsService           { return &paymentsService{c: c} }
func (c *client) Deposits() DepositsService           { return &depositsService{c: c} }
func (c *client) Rates() RatesService                 { return &ratesService{c: c} }
func (c *client) Webhooks() WebhooksService           { return &webhooksService{c: c} }

// ClientID implements Client.
func (c *client) ClientID(ctx context.Context) (string, error) {
	if _, err := c.ensureToken(ctx); err != nil {
		return "", err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clientID, nil
}

// Close implements Client.
func (c *client) Close() error {
	c.http.CloseIdleConnections()
	return nil
}

package capay

import (
	"context"
	"strings"
)

// AccountsService exposes balances and virtual accounts.
type AccountsService interface {
	// Balances returns your available balance per currency, or a customer's
	// when the filter names a ClientID.
	//
	// There is one figure per currency — no pending/total breakdown. Deposits
	// join the available balance once processed, including any settled
	// conversion bought in that currency.
	Balances(ctx context.Context, filter BalanceFilter) (*Page[Balance], error)

	// VirtualAccounts lists the virtual accounts — the real bank details you
	// hand to payers so they can send you funds.
	VirtualAccounts(ctx context.Context, filter VirtualAccountFilter) (*Page[VirtualAccount], error)

	// CreateVirtualAccount opens a new virtual account for the named client
	// (yourself or a customer) in a collection currency.
	//
	// The API answers with a list; in practice it holds the one account
	// created, but the slice is returned as-is so nothing is dropped.
	CreateVirtualAccount(ctx context.Context, req CreateVirtualAccountRequest, opts ...RequestOption) ([]VirtualAccount, error)

	// SetVirtualAccountPayID assigns or replaces the PayID on a virtual
	// account. Your domain must have been verified by Corporate Alliance
	// first; only email-type PayIDs are supported. Setting the PayID it
	// already has is a 400.
	SetVirtualAccountPayID(ctx context.Context, id, payIDEmail string, opts ...RequestOption) (*VirtualAccount, error)

	// DisableVirtualAccount stops a virtual account collecting.
	DisableVirtualAccount(ctx context.Context, id string, opts ...RequestOption) (*VirtualAccount, error)

	// ActivateVirtualAccount re-enables a disabled virtual account.
	ActivateVirtualAccount(ctx context.Context, id string, opts ...RequestOption) (*VirtualAccount, error)
}

// accountsService implements AccountsService.
type accountsService struct{ c *client }

// Balance is one currency's available balance.
type Balance struct {
	// ID is the balance record's id.
	ID string `json:"id"`

	// ClientID is the Client or Customer the balance belongs to.
	ClientID string `json:"clientId"`

	// CurrencyCode is the ISO 4217 code.
	CurrencyCode string `json:"currencyCode"`

	// Balance is the available balance in that currency.
	Balance float64 `json:"balance"`

	// CreatedTime is when the balance record was created.
	CreatedTime Time `json:"createdTime"`
}

// BalanceFilter narrows a Balances call. The zero value returns every
// currency you hold.
type BalanceFilter struct {
	ListOptions

	// CurrencyCodes restricts the result to these ISO 4217 codes.
	CurrencyCodes []string

	// ClientID scopes the query to a customer. Empty means yourself.
	ClientID string
}

// Balances implements AccountsService.
func (s *accountsService) Balances(ctx context.Context, filter BalanceFilter) (*Page[Balance], error) {
	q := newQuery().
		page(filter.ListOptions).
		str("currencyCodes", strings.Join(filter.CurrencyCodes, ",")).
		str("clientId", filter.ClientID)

	var out Page[Balance]
	if err := s.c.do(ctx, requestSpec{api: apiBalances, query: q.values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// VirtualAccountStatus is a virtual account's state.
type VirtualAccountStatus string

const (
	VirtualAccountActive   VirtualAccountStatus = "ACTIVE"
	VirtualAccountInactive VirtualAccountStatus = "INACTIVE"
)

// VirtualAccount is a set of real bank account details that a payer can send
// funds to. Incoming money becomes a Deposit.
type VirtualAccount struct {
	// ID is the virtual account's id, the value Deposit.AccountID refers to.
	ID string `json:"id"`

	// ClientID is the Client or Customer the account belongs to.
	ClientID string `json:"clientId"`

	// CurrencyCode is the collection currency, or empty for a multi-currency
	// account (the API sends null).
	CurrencyCode string `json:"currencyCode"`

	// AccountName is the account holder's name as payers will see it.
	AccountName string `json:"accountName"`

	// BankName, BankType, BankCountryCode and BankAddress describe the bank.
	BankName        string   `json:"bankName"`
	BankType        BankType `json:"bankType"`
	BankCountryCode string   `json:"bankCountryCode"`
	BankAddress     Address  `json:"bankAddress"`

	// RoutingNoType and RoutingNumber are the routing identifier (BSB for an
	// Australian account). RoutingNumber is null for some account types.
	RoutingNoType RoutingNoType `json:"routingNoType"`
	RoutingNumber string        `json:"routingNumber"`

	// AccountNumber is the account number.
	AccountNumber string `json:"accountNumber"`

	// PayID is the PayID, if one is assigned.
	PayID string `json:"payId"`

	// Status is ACTIVE or INACTIVE.
	Status VirtualAccountStatus `json:"status"`

	// CreatedTime is when the account was created.
	CreatedTime Time `json:"createdTime"`
}

// VirtualAccountFilter narrows a VirtualAccounts call.
type VirtualAccountFilter struct {
	ListOptions

	// CurrencyCode restricts the result to one collection currency.
	CurrencyCode string

	// ClientID scopes the query to a customer. Empty means yourself.
	ClientID string
}

// VirtualAccounts implements AccountsService.
func (s *accountsService) VirtualAccounts(ctx context.Context, filter VirtualAccountFilter) (*Page[VirtualAccount], error) {
	q := newQuery().
		page(filter.ListOptions).
		str("currencyCode", filter.CurrencyCode).
		str("clientId", filter.ClientID)

	var out Page[VirtualAccount]
	if err := s.c.do(ctx, requestSpec{api: apiVirtualAccountList, query: q.values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateVirtualAccountRequest opens a virtual account.
type CreateVirtualAccountRequest struct {
	// ClientID is the Client or Customer the account is created for. Unlike
	// most calls this is required — pass your own id (Client.ClientID) to
	// create one for yourself.
	ClientID string `json:"clientId"`

	// CurrencyCode is the collection currency.
	CurrencyCode string `json:"currencyCode"`

	// PayIDEmail optionally assigns a PayID at creation. The local part must
	// be 6–32 characters of lowercase letters, digits, underscores and dots,
	// and your domain must be verified by Corporate Alliance.
	PayIDEmail string `json:"payidEmail,omitempty"`
}

// CreateVirtualAccount implements AccountsService.
func (s *accountsService) CreateVirtualAccount(ctx context.Context, req CreateVirtualAccountRequest, opts ...RequestOption) ([]VirtualAccount, error) {
	if req.ClientID == "" {
		return nil, invalidInput("ClientID is required to create a virtual account")
	}
	if req.CurrencyCode == "" {
		return nil, invalidInput("CurrencyCode is required to create a virtual account")
	}

	spec := requestSpec{api: apiVirtualAccountCreate, body: req}
	if err := spec.apply(opts); err != nil {
		return nil, err
	}

	var out []VirtualAccount
	spec.result = &out
	if err := s.c.do(ctx, spec); err != nil {
		return nil, err
	}
	return out, nil
}

// payIDRequest is the body of the PayID update.
type payIDRequest struct {
	PayIDEmail string `json:"payidEmail"`
}

// SetVirtualAccountPayID implements AccountsService.
func (s *accountsService) SetVirtualAccountPayID(ctx context.Context, id, payIDEmail string, opts ...RequestOption) (*VirtualAccount, error) {
	if id == "" {
		return nil, invalidInput("virtual account id is required")
	}
	if payIDEmail == "" {
		return nil, invalidInput("PayID email is required")
	}
	return s.patch(ctx, apiVirtualAccountPayID, id, payIDRequest{PayIDEmail: payIDEmail}, opts)
}

// DisableVirtualAccount implements AccountsService.
func (s *accountsService) DisableVirtualAccount(ctx context.Context, id string, opts ...RequestOption) (*VirtualAccount, error) {
	if id == "" {
		return nil, invalidInput("virtual account id is required")
	}
	return s.patch(ctx, apiVirtualAccountDisable, id, nil, opts)
}

// ActivateVirtualAccount implements AccountsService.
func (s *accountsService) ActivateVirtualAccount(ctx context.Context, id string, opts ...RequestOption) (*VirtualAccount, error) {
	if id == "" {
		return nil, invalidInput("virtual account id is required")
	}
	return s.patch(ctx, apiVirtualAccountActivate, id, nil, opts)
}

// patch is the shared shape of the three PATCH /accounts/virtual/{id}/… calls.
func (s *accountsService) patch(ctx context.Context, a api, id string, body any, opts []RequestOption) (*VirtualAccount, error) {
	spec := requestSpec{api: a, pathParams: map[string]string{"id": id}, body: body}
	if err := spec.apply(opts); err != nil {
		return nil, err
	}
	var out VirtualAccount
	spec.result = &out
	if err := s.c.do(ctx, spec); err != nil {
		return nil, err
	}
	return &out, nil
}

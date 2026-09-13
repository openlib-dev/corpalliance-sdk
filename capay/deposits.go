package capay

import (
	"context"
	"time"
)

// DepositsService exposes inbound deposits. A deposit is money received into
// one of your virtual accounts; there is no "create deposit" — you share the
// virtual account details with a payer, and when funds arrive the API records
// a deposit and credits the balance. Every status change fires a DEPOSIT
// webhook.
//
// Where the money lands depends on which virtual account received it: a House
// Account virtual account credits you directly (one DEPOSIT ledger line); a
// customer's Sub-Account virtual account is COBO — the Sub-Account is credited
// and the funds are swept to your House Account (a DEPOSIT and a TRANSFER).
type DepositsService interface {
	// List returns deposits matching the filter.
	List(ctx context.Context, filter DepositFilter) (*Page[Deposit], error)

	// Simulate creates a production-like deposit against one of your virtual
	// accounts. Sandbox only; the maximum amount is 100. Use it to trigger
	// webhooks, fund WAITING FUNDS payments and test deposit retrieval.
	Simulate(ctx context.Context, req SimulateDepositRequest, opts ...RequestOption) error
}

// depositsService implements DepositsService.
type depositsService struct{ c *client }

// DepositType is how a deposit was settled.
//
// The live sandbox also reports "MANUAL DEPOSIT" for a deposit booked by
// Corporate Alliance staff, which the documentation's enum omits.
type DepositType string

const (
	DepositWireTransfer DepositType = "WIRE TRANSFER"
	DepositPayID        DepositType = "PAYID"
	DepositManual       DepositType = "MANUAL DEPOSIT"
)

// DepositStatus is a deposit's lifecycle state.
type DepositStatus string

const (
	DepositScheduled             DepositStatus = "SCHEDULED"
	DepositPendingDirectDebit    DepositStatus = "PENDING DIRECT DEBIT"
	DepositDirectDebitProcessing DepositStatus = "DIRECT DEBIT PROCESSING"
	DepositCompleted             DepositStatus = "COMPLETED"
	DepositCancelled             DepositStatus = "CANCELLED"
	DepositRejected              DepositStatus = "REJECTED"
	DepositPendingRefund         DepositStatus = "PENDING REFUND"
	DepositRefunding             DepositStatus = "REFUNDING"
	DepositRefunded              DepositStatus = "REFUNDED"
	DepositRefundCancelled       DepositStatus = "REFUND CANCELLED"
	DepositRefundRejected        DepositStatus = "REFUND REJECTED"
)

// Deposit is money received into a virtual account.
type Deposit struct {
	// ID is the deposit's id.
	ID string `json:"id"`

	// ClientID is the Client or Customer that received the funds; ClientName
	// its name.
	ClientID   string `json:"clientId"`
	ClientName string `json:"clientName"`

	// AccountID is the virtual account credited. Empty (null) on a manual
	// deposit booked without one.
	AccountID string `json:"accountId"`

	// CurrencyCode and Amount are what was received.
	CurrencyCode string  `json:"currencyCode"`
	Amount       float64 `json:"amount"`

	// Type is how the deposit was settled.
	Type DepositType `json:"type"`

	// ReferenceNo is the human-readable reference, e.g. "20240726-FA8JZ4".
	ReferenceNo string `json:"referenceNo"`

	// DepositDate is the value date.
	DepositDate Date `json:"depositDate"`

	// Note is the deposit reference, if any.
	Note string `json:"note"`

	// Payer describes who sent the funds.
	Payer *Payer `json:"payer"`

	// Status is the lifecycle state.
	Status DepositStatus `json:"status"`

	// CreatedTime and UpdatedTime are the record's timestamps.
	CreatedTime Time `json:"createdTime"`
	UpdatedTime Time `json:"updatedTime"`
}

// Payer is the sending side of a deposit.
type Payer struct {
	// Name is the payer's name; it may differ from AccountName.
	Name string `json:"name"`

	// Amount and CurrencyCode are what the payer sent. On a non-local payment
	// the originating amount may differ from what was received.
	Amount       float64 `json:"amount"`
	CurrencyCode string  `json:"currencyCode"`

	// AccountName, AccountNumber and RoutingNumber identify the payer's
	// account.
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	RoutingNumber string `json:"routingNumber"`

	// ValueDate and CreateDate are the payer-side dates.
	ValueDate  Time `json:"valueDate"`
	CreateDate Time `json:"createDate"`

	// PaymentReference and AdditionalInfo are the payer's reference and
	// description.
	PaymentReference string `json:"paymentReference"`
	AdditionalInfo   string `json:"additionalInfo"`
}

// DepositFilter narrows a List call.
type DepositFilter struct {
	ListOptions

	// ReferenceNo matches the human-readable reference exactly.
	ReferenceNo string

	// CurrencyCode, ClientID, AccountID and Status restrict by field.
	CurrencyCode string
	ClientID     string
	AccountID    string
	Status       DepositStatus

	// DepositDateFrom/To bound the deposit date, inclusive.
	DepositDateFrom Date
	DepositDateTo   Date

	// CreatedFrom/To and UpdatedFrom/To bound the record timestamps.
	CreatedFrom time.Time
	CreatedTo   time.Time
	UpdatedFrom time.Time
	UpdatedTo   time.Time
}

// List implements DepositsService.
func (s *depositsService) List(ctx context.Context, filter DepositFilter) (*Page[Deposit], error) {
	q := newQuery().
		page(filter.ListOptions).
		str("referenceNo", filter.ReferenceNo).
		str("currencyCode", filter.CurrencyCode).
		str("clientId", filter.ClientID).
		str("accountId", filter.AccountID).
		str("status", string(filter.Status)).
		date("depositDateFrom", filter.DepositDateFrom).
		date("depositDateTo", filter.DepositDateTo).
		time("createdTimeFrom", filter.CreatedFrom).
		time("createdTimeTo", filter.CreatedTo).
		time("updatedTimeFrom", filter.UpdatedFrom).
		time("updatedTimeTo", filter.UpdatedTo)

	var out Page[Deposit]
	if err := s.c.do(ctx, requestSpec{api: apiDepositList, query: q.values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// MaxSimulatedDepositAmount is the largest amount the sandbox simulator
// accepts.
const MaxSimulatedDepositAmount = 100

// SimulateDepositRequest drives the sandbox deposit simulator.
type SimulateDepositRequest struct {
	// Amount to credit, at most two decimal places and at most 100.
	Amount float64 `json:"amount"`

	// AccountNumber and RoutingNumber identify the virtual account to credit
	// — the values from VirtualAccount.AccountNumber and .RoutingNumber.
	AccountNumber string `json:"accountNumber"`
	RoutingNumber string `json:"routingNumber"`

	// CurrencyCode is required for a multi-currency virtual account.
	CurrencyCode string `json:"currencyCode,omitempty"`

	// Optional payer details, as they would arrive on a real deposit.
	Name               string `json:"name,omitempty"`
	AccountName        string `json:"accountName,omitempty"`
	PayerAccountNumber string `json:"payerAccountNumber,omitempty"`
	PayerRoutingNumber string `json:"payerRoutingNumber,omitempty"`
	PaymentReference   string `json:"paymentReference,omitempty"`
	AdditionalInfo     string `json:"additionalInfo,omitempty"`
}

// simulateResponse is the simulator's acknowledgement.
type simulateResponse struct {
	Message string `json:"message"`
}

// Simulate implements DepositsService.
func (s *depositsService) Simulate(ctx context.Context, req SimulateDepositRequest, opts ...RequestOption) error {
	switch {
	case req.Amount <= 0:
		return invalidInput("Amount must be positive")
	case req.Amount > MaxSimulatedDepositAmount:
		return invalidInput("Amount %v exceeds the simulator's maximum of %d", req.Amount, MaxSimulatedDepositAmount)
	case req.AccountNumber == "" || req.RoutingNumber == "":
		return invalidInput("AccountNumber and RoutingNumber are required")
	}
	if s.c.env == Production && s.c.baseURL == "" {
		return invalidInput("Simulate is available in Sandbox only")
	}
	var out simulateResponse
	return s.c.mutate(ctx, apiDepositSimulate, nil, req, &out, opts)
}

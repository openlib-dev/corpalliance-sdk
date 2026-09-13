package capay

import (
	"context"
	"time"
)

// TransactionsService exposes the ledger: every movement that changed a
// balance.
type TransactionsService interface {
	// List returns ledger entries — payments, deposits, conversions, fees and
	// the internal TRANSFER entries that move funds between your House Account
	// and a customer's Sub-Account. Use it to reconcile and to trace how a
	// balance reached its current value.
	List(ctx context.Context, filter TransactionFilter) (*Page[Transaction], error)
}

// transactionsService implements TransactionsService.
type transactionsService struct{ c *client }

// TransactionType classifies a ledger entry.
type TransactionType string

const (
	TransactionPayment       TransactionType = "PAYMENT"
	TransactionDeposit       TransactionType = "DEPOSIT"
	TransactionConversion    TransactionType = "CONVERSION"
	TransactionTransfer      TransactionType = "TRANSFER"
	TransactionAccountFee    TransactionType = "ACCOUNT FEE"
	TransactionDrawdown      TransactionType = "DRAWDOWN"
	TransactionTradeCloseOut TransactionType = "TRADE CLOSE OUT"

	// Deprecated: OPTION CLOSE OUT and DRAWDOWN REPAYMENT are documented as
	// deprecated and should not be relied on for new integrations.
	TransactionOptionCloseOut    TransactionType = "OPTION CLOSE OUT"
	TransactionDrawdownRepayment TransactionType = "DRAWDOWN REPAYMENT"
)

// Transaction is one ledger line.
type Transaction struct {
	// TransactionType classifies the movement.
	TransactionType TransactionType `json:"transactionType"`

	// CurrencyCode is the ISO 4217 code.
	CurrencyCode string `json:"currencyCode"`

	// ClientID is the Client or Customer the entry belongs to.
	ClientID string `json:"clientId"`

	// Amount is the movement, positive or negative.
	Amount float64 `json:"amount"`

	// Balance is the balance after this entry.
	Balance float64 `json:"balance"`

	// RelatedTransactionID is the id of the source record — the Payment or
	// Deposit that caused this entry.
	RelatedTransactionID string `json:"relatedTransactionId"`

	// RelatedTransactionReferenceNo is the human-readable reference of that
	// source record, e.g. "20251212-PEDT4D".
	RelatedTransactionReferenceNo string `json:"relatedTransactionReferenceNo"`

	// ReferenceID is your external reference on the source record, if you
	// supplied one.
	ReferenceID string `json:"referenceId"`

	// TransactionTime is when the balance was updated. Note the live API
	// sends this as "2026-09-11 05:59:48" rather than ISO 8601; Time handles
	// both.
	TransactionTime Time `json:"transactionTime"`
}

// TransactionFilter narrows a List call.
type TransactionFilter struct {
	ListOptions

	// ClientID scopes the query to a customer. Empty means yourself.
	ClientID string

	// CurrencyCode restricts to one currency.
	CurrencyCode string

	// TransactionType restricts to one type.
	TransactionType TransactionType

	// From and To bound transactionTime, inclusive. Sent as ISO 8601 in UTC.
	From time.Time
	To   time.Time
}

// List implements TransactionsService.
func (s *transactionsService) List(ctx context.Context, filter TransactionFilter) (*Page[Transaction], error) {
	if !filter.From.IsZero() && !filter.To.IsZero() && filter.To.Before(filter.From) {
		return nil, invalidInput("To (%s) is before From (%s)", filter.To, filter.From)
	}

	q := newQuery().
		page(filter.ListOptions).
		str("clientId", filter.ClientID).
		str("currencyCode", filter.CurrencyCode).
		str("transactionType", string(filter.TransactionType)).
		time("transactionTimeFrom", filter.From).
		time("transactionTimeTo", filter.To)

	var out Page[Transaction]
	if err := s.c.do(ctx, requestSpec{api: apiTransactions, query: q.values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

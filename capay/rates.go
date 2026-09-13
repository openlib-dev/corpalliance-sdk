package capay

import "context"

// RatesService exposes the indicative daily rate. For a firm, tradeable rate
// see PaymentsService.CreateQuotation.
type RatesService interface {
	// Daily returns the current reference rate for a currency pair. Both
	// codes are required. The rate is indicative only — the rate applied to a
	// payment is the one locked by its quotation.
	Daily(ctx context.Context, baseCurrencyCode, quoteCurrencyCode string) (*Rate, error)
}

// ratesService implements RatesService.
type ratesService struct{ c *client }

// Rate is an indicative exchange rate.
type Rate struct {
	// CurrencyPair is the pair as the API names it, e.g. "AUDUSD".
	CurrencyPair string `json:"currencyPair"`

	// BaseCurrencyCode is the buy currency; QuoteCurrencyCode the sell.
	BaseCurrencyCode  string `json:"baseCurrencyCode"`
	QuoteCurrencyCode string `json:"quoteCurrencyCode"`

	// ExchangeRate is the indicative rate.
	ExchangeRate float64 `json:"exchangeRate"`

	// TargetDate is the date the rate applies to.
	TargetDate Time `json:"targetDate"`
}

// Daily implements RatesService.
func (s *ratesService) Daily(ctx context.Context, baseCurrencyCode, quoteCurrencyCode string) (*Rate, error) {
	if baseCurrencyCode == "" || quoteCurrencyCode == "" {
		return nil, invalidInput("both baseCurrencyCode and quoteCurrencyCode are required")
	}

	q := newQuery().
		str("baseCurrencyCode", baseCurrencyCode).
		str("quoteCurrencyCode", quoteCurrencyCode)

	var out Rate
	if err := s.c.do(ctx, requestSpec{api: apiRateDaily, query: q.values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

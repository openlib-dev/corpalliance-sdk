# corpalliance-sdk

An unofficial Go SDK for the **Corporate Alliance CAPAY Client API** — the
REST/JSON API that lets a Corporate Alliance client's own software hold
balances across currencies, send local and SWIFT payments, collect deposits
through virtual accounts, lock FX quotations and receive webhook notifications.

Implements the CAPAY Client API **v1.0.0**, published at
<https://developer.corporatealliance.com/client/intro>, verified end to end
against the sandbox at `api.sandbox.capay.com.au`.

```bash
go get github.com/openlib-dev/corpalliance-sdk
```

## Scope

| Service | Covered |
| --- | --- |
| Authentication | `apiUser` + `apiKey` → JWT, cached and refreshed automatically |
| Accounts | balances, virtual accounts (list, create, PayID, disable, activate) |
| Transactions | the ledger |
| Beneficiaries | business and personal: create, list, get, update, delete, validate, Confirmation of Payee |
| Customers | the underlying customers you act for (the API's `/clients`): business and personal CRUD |
| Payments | create, create with inline beneficiary, FX quotation, list, get, update, cancel |
| Deposits | list, sandbox simulation |
| Rates | indicative daily rate |
| Webhooks | subscriptions, plus `DecryptWebhook` for the AES-256-CBC payloads |

The Program Manager API is the same surface under a different role and is not
covered.

## Quick start

```go
import "github.com/openlib-dev/corpalliance-sdk/capay"

c := capay.New(os.Getenv("CA_API_USER"), os.Getenv("CA_API_KEY"), capay.Sandbox)
defer c.Close()

balances, err := c.Accounts().Balances(ctx, capay.BalanceFilter{})
if err != nil {
    return err
}
for _, b := range balances.Data {
    fmt.Printf("%s %.2f\n", b.CurrencyCode, b.Balance)
}
```

`New` performs no network I/O; the first token is fetched on the first call.
Tokens live 60 minutes and are cached, refreshed shortly before expiry, and
refreshed once more if the API ever answers 401.

### Paying someone

```go
ben, err := c.Beneficiaries().CreateBusiness(ctx, capay.BusinessBeneficiaryRequest{
    CountryCode: "AU",
    Name:        "ABC Pty Ltd",
    ReferenceID: "vendor-42",            // your own key; a repeat is rejected
    BankAccount: &capay.BankAccount{
        CountryCode: "AU", BankType: capay.BankTypeLocal, BankName: "ABC Bank",
        AccountName: "ABC Pty Ltd", RoutingNoType: capay.RoutingBSB,
        RoutingNumber: "012999", AccountNumber: "123456", CurrencyCode: "AUD",
    },
}, capay.WithIdempotencyKey("ben-vendor-42"))

payment, err := c.Payments().Create(ctx, capay.CreatePaymentRequest{
    BeneficiaryID:    ben.ID,
    CurrencyCode:     "AUD",
    Amount:           1500.00,
    ChargeType:       capay.ChargeShared,
    PurposeCode:      capay.PurposeServices,
    SourceOfFunds:    capay.SourceBusinessIncome, // required for local payments
    PaymentReference: "Invoice 123",
    PaymentDate:      capay.Today(),
    ReferenceID:      "inv-123",
}, capay.WithIdempotencyKey("pay-inv-123"))
```

Always pass `WithIdempotencyKey` on payments. A replayed key is answered with
`ErrConflict` rather than a second payment, and the SDK never retries a
mutating request on its own — see [`docs/decisions.md`](docs/decisions.md).

For a cross-currency payment, lock a rate first:

```go
q, err := c.Payments().CreateQuotation(ctx, capay.QuotationRequest{
    BaseCurrencyCode: "USD", QuoteCurrencyCode: "AUD", Date: capay.Today(),
})
// then set SellCurrencyCode: "AUD", QuotationID: q.QuotationID on the payment
```

### Acting for an underlying customer

Every request and filter that accepts it has a `ClientID` field. Leave it
empty to act as yourself; set it to a customer's `clientId` (from
`c.Customers()`) to create beneficiaries they own, pay on their behalf
(POBO) or query their deposits (COBO).

### Receiving webhooks

```go
func handler(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    n, err := capay.DecryptWebhook(body, os.Getenv("CA_API_SECRET"))
    if err != nil {
        http.Error(w, "bad payload", http.StatusBadRequest)
        return
    }
    // deduplicate on n.MessageID — delivery is at-least-once
    switch n.EventType {
    case capay.EventPayment:
        p, _ := n.Payment()
        // …
    case capay.EventDeposit:
        d, _ := n.Deposit()
        // …
    }
    w.WriteHeader(http.StatusOK)
}
```

### Errors

Every non-2xx response is an `*APIError` wrapping a sentinel:

```go
if errors.Is(err, capay.ErrNotFound) { … }
if errors.Is(err, capay.ErrConflict) { … }   // idempotency key replayed

var apiErr *capay.APIError
if errors.As(err, &apiErr) {
    log.Println(apiErr.StatusCode, apiErr.Messages) // one entry per failed rule
}
```

Requests that cannot satisfy a documented rule fail locally with
`ErrInvalidInput` before any network call.

### Pagination

List calls return a `Page[T]` with `Meta.TotalCount`; use `HasMore()` and
`NextSkip()` to walk it:

```go
opts := capay.ListOptions{Limit: 100}
for {
    page, err := c.Payments().List(ctx, capay.PaymentFilter{ListOptions: opts})
    …
    if !page.HasMore() { break }
    opts.Skip = page.NextSkip()
}
```

## Configuration

| Option | Default |
| --- | --- |
| `WithTimeout(d)` | 60s per attempt |
| `WithRetry(policy)` / `WithoutRetry()` | 3 attempts, 500ms base, 5s cap; reads only |
| `WithTokenRefreshMargin(d)` | 60s before expiry |
| `WithHTTPClient(*http.Client)` | bounded pool, TLS 1.2+, HTTP/2 |
| `WithBaseURL(url)` | environment default; the seam for tests |

## Testing

```bash
go test ./...                                   # unit tests, no network
CA_API_USER=… CA_API_KEY=… go test ./capay/ -run Integration -v   # read-only sandbox
CA_TEST_WRITE=1 …                               # also creates/cancels a 1 AUD payment
```

Copy `.env.example` to `.env` and fill it in; quote the values with single
quotes.

## Examples

- [`examples/basic`](examples/basic/main.go) — balances, virtual accounts, ledger
- [`examples/payment`](examples/payment/main.go) — Confirmation of Payee, beneficiary, payment, cancel

## License

Apache 2.0. Not affiliated with or endorsed by Corporate Alliance.

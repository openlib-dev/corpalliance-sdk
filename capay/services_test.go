package capay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// capture records the last request the handler saw and answers with body.
type capture struct {
	method string
	path   string
	query  string
	body   map[string]any
}

func captureHandler(t *testing.T, status int, body string, got *capture) http.HandlerFunc {
	t.Helper()
	return tokenHandler(t, "tok", 3600, func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.body = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&got.body)
		}
		writeJSON(w, status, body)
	})
}

func TestBalancesDecodeLiveShape(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, balancesBody, &got))

	page, err := c.Accounts().Balances(context.Background(), BalanceFilter{
		CurrencyCodes: []string{"AUD", "USD"},
		ClientID:      "cust-1",
		ListOptions:   ListOptions{Limit: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.query != "clientId=cust-1&currencyCodes=AUD%2CUSD&limit=5&skip=0" {
		t.Errorf("query = %q", got.query)
	}
	if len(page.Data) != 1 || page.Data[0].Balance != 1_000_000 || page.Data[0].CurrencyCode != "AUD" {
		t.Errorf("page = %+v", page)
	}
	if page.Meta.TotalCount != 1 || page.Meta.Timestamp.IsZero() {
		t.Errorf("meta = %+v", page.Meta)
	}
}

func TestVirtualAccountDecodesLiveShape(t *testing.T) {
	const body = `{"meta":{"totalCount":1,"skip":0,"limit":10,"timestamp":"2026-09-12T06:39:02.020Z"},"data":[{"id":"2ce9c5aa","clientId":"e0874c5d","currencyCode":"AUD","accountName":"CHU PAY PTY LTD","bankName":"Corporate Alliance","bankType":"LOCAL","bankCountryCode":"AU","routingNoType":"BSB","accountNumber":"7384248","routingNumber":"570002","payId":"pay.chupay@capay.com","status":"ACTIVE","createdTime":"2026-09-11T04:32:13.566Z","bankAddress":{"street":"Suite 1204 219-227 Elizabeth Street","city":"Sydney","state":"New South Wales","zip":"2000"}}]}`
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, body, &got))

	page, err := c.Accounts().VirtualAccounts(context.Background(), VirtualAccountFilter{})
	if err != nil {
		t.Fatal(err)
	}
	va := page.Data[0]
	if va.RoutingNoType != RoutingBSB || va.RoutingNumber != "570002" || va.Status != VirtualAccountActive {
		t.Errorf("va = %+v", va)
	}
	if va.BankAddress.City != "Sydney" {
		t.Errorf("bank address = %+v", va.BankAddress)
	}

	// A multi-currency account has a null currency; that must decode.
	var multi VirtualAccount
	if err := json.Unmarshal([]byte(`{"id":"x","currencyCode":null,"routingNumber":null,"payId":null}`), &multi); err != nil {
		t.Fatal(err)
	}
	if multi.CurrencyCode != "" {
		t.Errorf("null currency = %q", multi.CurrencyCode)
	}
}

func TestVirtualAccountPatchCalls(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, `{"id":"va1","status":"INACTIVE"}`, &got))

	va, err := c.Accounts().DisableVirtualAccount(context.Background(), "va1", WithIdempotencyKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPatch || got.path != "/accounts/virtual/va1/disable" || got.body != nil {
		t.Errorf("request = %+v", got)
	}
	if va.Status != VirtualAccountInactive {
		t.Errorf("status = %q", va.Status)
	}

	if _, err := c.Accounts().SetVirtualAccountPayID(context.Background(), "va1", "pay.x@example.com"); err != nil {
		t.Fatal(err)
	}
	if got.path != "/accounts/virtual/va1/payid" || got.body["payidEmail"] != "pay.x@example.com" {
		t.Errorf("request = %+v", got)
	}
}

func TestCreateVirtualAccountRequiresClientID(t *testing.T) {
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, nil))
	_, err := c.Accounts().CreateVirtualAccount(context.Background(), CreateVirtualAccountRequest{CurrencyCode: "AUD"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v", err)
	}
}

func TestTransactionsDecodeLiveShape(t *testing.T) {
	// Observed live: transactionTime without a T or zone, referenceId null.
	const body = `{"meta":{"totalCount":1,"skip":0,"limit":2,"timestamp":"2026-09-12T06:39:20.944Z"},"data":[{"transactionType":"DEPOSIT","currencyCode":"AUD","clientId":"e0874c5d","amount":1000000,"balance":1000000,"relatedTransactionId":"b7cc31d4","relatedTransactionReferenceNo":"20260911-F9RZIV","referenceId":null,"transactionTime":"2026-09-11 05:59:48"}]}`
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, body, &got))

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	page, err := c.Transactions().List(context.Background(), TransactionFilter{
		TransactionType: TransactionDeposit, From: from, To: from.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.query != "transactionTimeFrom=2026-09-01T00%3A00%3A00.000Z&transactionTimeTo=2026-09-02T00%3A00%3A00.000Z&transactionType=DEPOSIT" {
		t.Errorf("query = %q", got.query)
	}
	tx := page.Data[0]
	want := time.Date(2026, 9, 11, 5, 59, 48, 0, time.UTC)
	if !tx.TransactionTime.Equal(want) {
		t.Errorf("transactionTime = %v (raw %q), want %v", tx.TransactionTime.Time, tx.TransactionTime.Raw, want)
	}
	if tx.ReferenceID != "" || tx.RelatedTransactionReferenceNo != "20260911-F9RZIV" {
		t.Errorf("tx = %+v", tx)
	}

	_, err = c.Transactions().List(context.Background(), TransactionFilter{From: from, To: from.Add(-time.Hour)})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("reversed range: %v", err)
	}
}

func TestBeneficiaryRequiresAccountOrFastID(t *testing.T) {
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, nil))
	_, err := c.Beneficiaries().CreateBusiness(context.Background(), BusinessBeneficiaryRequest{CountryCode: "AU", Name: "ABC"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v", err)
	}
	_, err = c.Beneficiaries().CreatePersonal(context.Background(), PersonalBeneficiaryRequest{CountryCode: "AU", UniquePayID: "1", FirstName: "A"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("missing last name: %v", err)
	}
}

func TestBeneficiaryCreateAndUpdateWire(t *testing.T) {
	const body = `{"id":"b1","countryCode":"AU","name":"ABC Pty Ltd","isActive":true,"createdTime":"2026-09-12T00:00:00.000Z","bankAccount":{"countryCode":"AU","bankType":"LOCAL","bankName":"X","accountName":"ABC Pty Ltd","routingNoType":"BSB","routingNumber":"012999","accountNumber":"123456","currencyCode":"AUD"}}`
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, body, &got))

	req := BusinessBeneficiaryRequest{
		CountryCode: "AU",
		Name:        "ABC Pty Ltd",
		ClientID:    "cust-1",
		ReferenceID: "ref-1",
		BankAccount: &BankAccount{
			CountryCode: "AU", BankType: BankTypeLocal, BankName: "X", AccountName: "ABC Pty Ltd",
			RoutingNoType: RoutingBSB, RoutingNumber: "012999", AccountNumber: "123456", CurrencyCode: "AUD",
		},
	}
	b, err := c.Beneficiaries().CreateBusiness(context.Background(), req, WithIdempotencyKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/beneficiaries/business" || got.body["clientId"] != "cust-1" || got.body["referenceId"] != "ref-1" {
		t.Errorf("create request = %+v", got)
	}
	if _, ok := got.body["uniquePayId"]; ok {
		t.Error("empty uniquePayId must be omitted")
	}
	if b.ID != "b1" || !b.IsActive || b.BankAccount.RoutingNumber != "012999" {
		t.Errorf("beneficiary = %+v", b)
	}

	// Update is a PUT to the singular path, without the creation-only fields.
	if _, err := c.Beneficiaries().UpdateBusiness(context.Background(), "b1", req); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPut || got.path != "/beneficiaries/business/b1" {
		t.Errorf("update request = %+v", got)
	}
	for _, k := range []string{"clientId", "referenceId"} {
		if _, ok := got.body[k]; ok {
			t.Errorf("update must not send %s", k)
		}
	}

	// The list endpoint is plural.
	_, _ = c.Beneficiaries().ListBusinesses(context.Background(), BeneficiaryFilter{Name: "ABC"}) // path only
	if got.path != "/beneficiaries/businesses" || got.query != "name=ABC" {
		t.Errorf("list request = %+v", got)
	}
}

func TestConfirmationOfPayeeDecodes(t *testing.T) {
	// The v2 validators answer 201, and COP OTHERS for anything it cannot
	// check — both observed live.
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, `{"code":"COP CLOSE MATCHED","message":"Account close matched","copDetails":{"accountNames":[{"accountName":"ABC PTY LTD","matchScore":95}]}}`, &got))

	req := PersonalBeneficiaryRequest{CountryCode: "AU", FirstName: "A", LastName: "B", UniquePayID: "123"}
	res, err := c.Beneficiaries().ConfirmPersonalPayee(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/v2/beneficiaries/personals/validate" {
		t.Errorf("path = %q", got.path)
	}
	if res.Code != CoPCloseMatched || res.CoPDetails.AccountNames[0].MatchScore != 95 {
		t.Errorf("result = %+v", res)
	}
}

func TestValidateReturnsNilOnEmpty200(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, ``, &got))
	req := BusinessBeneficiaryRequest{CountryCode: "AU", Name: "A", UniquePayID: "1"}
	if err := c.Beneficiaries().ValidateBusiness(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got.path != "/beneficiaries/business/validate" {
		t.Errorf("path = %q", got.path)
	}
}

func TestPaymentLocalValidation(t *testing.T) {
	c, _ := newTestClient(t, tokenHandler(t, "tok", 3600, nil))
	ctx := context.Background()

	bad := func(mut func(*CreatePaymentRequest)) error {
		r := validPayment()
		mut(&r)
		_, err := c.Payments().Create(ctx, r)
		return err
	}
	cases := map[string]func(*CreatePaymentRequest){
		"amount below 1":         func(r *CreatePaymentRequest) { r.Amount = 0.5 },
		"three decimals":         func(r *CreatePaymentRequest) { r.Amount = 10.005 },
		"no beneficiary":         func(r *CreatePaymentRequest) { r.BeneficiaryID = "" },
		"no charge type":         func(r *CreatePaymentRequest) { r.ChargeType = "" },
		"no date":                func(r *CreatePaymentRequest) { r.PaymentDate = Date{} },
		"quotation without sell": func(r *CreatePaymentRequest) { r.QuotationID = "q" },
		"long reference":         func(r *CreatePaymentRequest) { r.PaymentReference = string(make([]byte, 65)) },
	}
	for name, mut := range cases {
		if err := bad(mut); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPaymentCreateWire(t *testing.T) {
	const body = `{"id":"p1","referenceNo":"20240726-PA8JZ4","currencyCode":"USD","sellCurrencyCode":"AUD","amount":10000,"chargeFee":10,"sellAmount":15000,"exchangeRate":0.66667,"currencyPair":"AUDUSD","status":"SCHEDULED","paymentDate":"2024-07-30","referenceId":null,"failureReason":null,"createdTime":"2023-12-12T01:23:33Z","updatedTime":"2023-12-12T01:23:33Z"}`
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, body, &got))

	req := validPayment()
	req.SellCurrencyCode, req.QuotationID = "AUD", "q1"
	req.ClientID = "cust-1"
	p, err := c.Payments().Create(context.Background(), req, WithIdempotencyKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if got.body["paymentDate"] != "2026-09-30" || got.body["clientId"] != "cust-1" || got.body["amount"] != float64(10) {
		t.Errorf("body = %+v", got.body)
	}
	if _, ok := got.body["invoiceDate"]; ok {
		t.Error("zero invoiceDate must be omitted")
	}
	if p.Status != PaymentScheduled || !p.Status.IsCancelable() || p.Status.IsFinal() {
		t.Errorf("status = %q", p.Status)
	}
	if p.ExchangeRate != 0.66667 || p.PaymentDate.String() != "2024-07-30" {
		t.Errorf("payment = %+v", p)
	}
}

func TestPaymentWithBeneficiaryWire(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, `{"id":"p1","status":"SCHEDULED"}`, &got))

	req := CreatePaymentWithBeneficiaryRequest{
		CurrencyCode: "AUD", Amount: 10, ChargeType: ChargeShared, PaymentReference: "x",
		PaymentDate: NewDate(2026, 9, 30), PurposeCode: PurposeGoods, SourceOfFunds: SourceBusinessIncome,
		Personal: &PersonalBeneficiaryRequest{CountryCode: "AU", FirstName: "A", LastName: "B", UniquePayID: "1"},
	}
	if _, err := c.Payments().CreateWithBeneficiary(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got.path != "/payments/with-beneficiary" || got.body["beneficiaryType"] != "PERSONAL" {
		t.Errorf("request = %+v", got)
	}
	ben, _ := got.body["beneficiary"].(map[string]any)
	if ben["firstName"] != "A" {
		t.Errorf("beneficiary = %+v", ben)
	}

	req.Business = &BusinessBeneficiaryRequest{CountryCode: "AU", Name: "X", UniquePayID: "1"}
	if _, err := c.Payments().CreateWithBeneficiary(context.Background(), req); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("both set: %v", err)
	}
	req.Business, req.Personal = nil, nil
	if _, err := c.Payments().CreateWithBeneficiary(context.Background(), req); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("neither set: %v", err)
	}
}

func TestPaymentCancelAndUpdateWire(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, `{"id":"p1","status":"CANCELLED"}`, &got))

	p, err := c.Payments().Cancel(context.Background(), "p1", "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPut || got.path != "/payments/cancel/p1" || got.body["reason"] != "customer request" {
		t.Errorf("request = %+v", got)
	}
	if p.Status != PaymentCancelled {
		t.Errorf("status = %q", p.Status)
	}

	_, err = c.Payments().Update(context.Background(), "p1", UpdatePaymentRequest{
		PaymentReference: "new", PaymentDate: NewDate(2026, 10, 1), PurposeCode: PurposeServices,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/payments/p1" || got.body["purposeCode"] != "SERVICES" {
		t.Errorf("request = %+v", got)
	}
}

func TestQuotationWire(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, `{"currencyPair":"AUDUSD","baseCurrencyCode":"USD","quoteCurrencyCode":"AUD","exchangeRate":0.681,"targetDate":"2024-08-05","quotationId":"q1","expiryTime":"2024-08-05T01:00:00.000Z"}`, &got))

	q, err := c.Payments().CreateQuotation(context.Background(), QuotationRequest{
		BaseCurrencyCode: "USD", QuoteCurrencyCode: "AUD", Date: NewDate(2024, 8, 5), FixedSide: FixedSideBuy, Amount: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.body["fixedSide"] != "buy" || got.body["date"] != "2024-08-05" {
		t.Errorf("body = %+v", got.body)
	}
	if q.QuotationID != "q1" || q.TargetDate.String() != "2024-08-05" || q.ExpiryTime.IsZero() {
		t.Errorf("quotation = %+v", q)
	}

	_, err = c.Payments().CreateQuotation(context.Background(), QuotationRequest{BaseCurrencyCode: "USD", QuoteCurrencyCode: "AUD", Date: NewDate(2024, 8, 5), Amount: 5})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("amount without side: %v", err)
	}
}

func TestPaymentListFilterWire(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, `{"meta":{},"data":[]}`, &got))
	_, err := c.Payments().List(context.Background(), PaymentFilter{
		Status: PaymentCompleted, PaymentDateFrom: NewDate(2026, 1, 1), ListOptions: ListOptions{Skip: 10, Limit: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.query != "limit=5&paymentDateFrom=2026-01-01&skip=10&status=COMPLETED" {
		t.Errorf("query = %q", got.query)
	}
}

func TestDepositsDecodeLiveShape(t *testing.T) {
	// Observed live: type "MANUAL DEPOSIT" (undocumented), accountId null.
	const body = `{"meta":{"totalCount":1,"skip":0,"limit":2,"timestamp":"2026-09-12T06:39:22.005Z"},"data":[{"id":"b7cc31d4","clientId":"e0874c5d","currencyCode":"AUD","amount":1000000,"type":"MANUAL DEPOSIT","referenceNo":"20260911-F9RZIV","depositDate":"2026-09-11","note":"test","createdTime":"2026-09-11T05:55:07.000Z","updatedTime":"2026-09-11T05:59:48.498Z","clientName":"CHU PAY PTY LTD","accountId":null,"payer":{"name":"WEILING HE","amount":1000000,"currencyCode":"AUD","accountName":"WEILING HE","accountNumber":"88888888","routingNumber":"888888","valueDate":"2026-09-11T00:00:00.000Z","createDate":"2026-09-11T00:00:00.000Z"},"status":"COMPLETED"}]}`
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, body, &got))

	page, err := c.Deposits().List(context.Background(), DepositFilter{Status: DepositCompleted})
	if err != nil {
		t.Fatal(err)
	}
	d := page.Data[0]
	if d.Type != DepositManual || d.AccountID != "" || d.Payer == nil || d.Payer.Name != "WEILING HE" {
		t.Errorf("deposit = %+v", d)
	}
	if d.DepositDate.String() != "2026-09-11" || d.Status != DepositCompleted {
		t.Errorf("deposit = %+v", d)
	}
}

func TestSimulateDepositGuards(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, `{"message":"Success"}`, &got))
	ctx := context.Background()

	ok := SimulateDepositRequest{Amount: 10, AccountNumber: "7384248", RoutingNumber: "570002", CurrencyCode: "AUD"}
	if err := c.Deposits().Simulate(ctx, ok); err != nil {
		t.Fatal(err)
	}
	if got.path != "/deposits/simulate" || got.body["amount"] != float64(10) {
		t.Errorf("request = %+v", got)
	}

	over := ok
	over.Amount = 101
	if err := c.Deposits().Simulate(ctx, over); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("over max: %v", err)
	}

	prod := New("u", "k", Production, WithoutRetry())
	if err := prod.Deposits().Simulate(ctx, ok); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("production: %v", err)
	}
}

func TestRatesWire(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 200, `{"currencyPair":"AUDUSD","baseCurrencyCode":"USD","quoteCurrencyCode":"AUD","exchangeRate":0.69221,"targetDate":"2026-06-16T00:00:00.000Z"}`, &got))
	r, err := c.Rates().Daily(context.Background(), "USD", "AUD")
	if err != nil {
		t.Fatal(err)
	}
	if got.query != "baseCurrencyCode=USD&quoteCurrencyCode=AUD" || r.ExchangeRate != 0.69221 {
		t.Errorf("query=%q rate=%+v", got.query, r)
	}
	if _, err := c.Rates().Daily(context.Background(), "USD", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("missing quote: %v", err)
	}
}

func TestCustomersWire(t *testing.T) {
	const body = `{"id":"cust-1","clientNo":"CAPAYAU-SYD-021233","uniquePayId":"6244120284213583","status":"PENDING APPROVAL","name":"ABC Pty Ltd","countryCode":"AU","address":{"city":"Sydney"},"createdTime":"2023-12-12T01:23:33Z","updatedTime":"2023-12-12T01:23:33Z"}`
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, body, &got))

	req := CreateBusinessCustomerRequest{
		CountryCode: "AU", Address: Address{City: "Sydney"}, Name: "ABC Pty Ltd", TradingName: "ABC",
		BusinessType: "Australian Proprietary Company", BusinessNumber: "66655577888",
		BeneficiaryOwnerFirstName: "John", BeneficiaryOwnerLastName: "Doe", RegisterDate: NewDate(2020, 7, 1),
	}
	cu, err := c.Customers().CreateBusiness(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/clients/business" || got.body["registerDate"] != "2020-07-01" {
		t.Errorf("request = %+v", got)
	}
	if cu.ID != "cust-1" || cu.Status != CustomerPendingApproval || cu.UniquePayID != "6244120284213583" || cu.Name != "ABC Pty Ltd" {
		t.Errorf("customer = %+v", cu)
	}

	_, _ = c.Customers().ListBusinesses(context.Background(), CustomerFilter{}) // path only
	if got.path != "/clients/businesses" {
		t.Errorf("list path = %q", got.path)
	}
	_, _ = c.Customers().ListPersonals(context.Background(), CustomerFilter{}) // path only
	if got.path != "/clients/personal" {
		t.Errorf("personal list path = %q", got.path)
	}

	if err := c.Customers().DeleteBusiness(context.Background(), "cust-1"); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodDelete || got.path != "/clients/business/cust-1" {
		t.Errorf("delete = %+v", got)
	}

	_, err = c.Customers().CreatePersonal(context.Background(), CreatePersonalCustomerRequest{CountryCode: "AU", FirstName: "A", LastName: "B", Nationality: "AU"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("missing DOB: %v", err)
	}
}

func TestWebhookSubscriptionWire(t *testing.T) {
	var got capture
	c, _ := newTestClient(t, captureHandler(t, 201, `{"id":"w1","endpoint":"https://x.example/hook","events":["PAYMENT","DEPOSIT"],"createdTime":"2023-12-12T01:23:33Z"}`, &got))

	w, err := c.Webhooks().Create(context.Background(), WebhookRequest{Endpoint: "https://x.example/hook", Events: []EventType{EventPayment, EventDeposit}})
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != "w1" || len(w.Events) != 2 {
		t.Errorf("webhook = %+v", w)
	}
	_, err = c.Webhooks().Create(context.Background(), WebhookRequest{Endpoint: "not a url", Events: []EventType{EventPayment}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("relative endpoint: %v", err)
	}
	_, err = c.Webhooks().Create(context.Background(), WebhookRequest{Endpoint: "https://x.example/hook"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("no events: %v", err)
	}
}

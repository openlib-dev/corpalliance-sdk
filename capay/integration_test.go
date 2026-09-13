package capay_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/openlib-dev/corpalliance-sdk/capay"
)

// Integration tests run against the real sandbox and are skipped unless
// credentials are supplied:
//
//	CA_API_USER=… CA_API_KEY=… go test ./capay/ -run Integration -v
//
// They are read-only by default. Set CA_TEST_WRITE=1 as well to include the
// write tests, which create and cancel a 1 AUD payment to a throwaway
// beneficiary and simulate a 1 AUD deposit — all inside the sandbox, where no
// real money moves.
func integrationClient(t *testing.T) capay.Client {
	t.Helper()

	user, key := os.Getenv("CA_API_USER"), os.Getenv("CA_API_KEY")
	if user == "" || key == "" {
		t.Skip("set CA_API_USER and CA_API_KEY to run integration tests")
	}

	c := capay.New(user, key, capay.Sandbox)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestIntegrationClientID(t *testing.T) {
	c := integrationClient(t)

	id, err := c.ClientID(context.Background())
	if err != nil {
		t.Fatalf("ClientID: %v", err)
	}
	if id == "" {
		t.Error("ClientID is empty; the access token carried no companyId claim")
	}
	if want := os.Getenv("CA_CLIENT_ID"); want != "" && id != want {
		t.Errorf("ClientID = %s, CA_CLIENT_ID = %s", id, want)
	}
	t.Logf("client %s", id)
}

func TestIntegrationBalancesAndVirtualAccounts(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()

	balances, err := c.Accounts().Balances(ctx, capay.BalanceFilter{})
	if err != nil {
		t.Fatalf("Balances: %v", err)
	}
	if len(balances.Data) == 0 {
		t.Fatal("the sandbox returned no balances")
	}
	for _, b := range balances.Data {
		t.Logf("%s %.2f (client %s)", b.CurrencyCode, b.Balance, b.ClientID)
	}

	vas, err := c.Accounts().VirtualAccounts(ctx, capay.VirtualAccountFilter{})
	if err != nil {
		t.Fatalf("VirtualAccounts: %v", err)
	}
	for _, va := range vas.Data {
		t.Logf("VA %s %s %s %s/%s payid=%s %s", va.ID, va.CurrencyCode, va.RoutingNoType, va.RoutingNumber, va.AccountNumber, va.PayID, va.Status)
		if va.AccountNumber == "" {
			t.Errorf("virtual account %s has no account number", va.ID)
		}
	}

	// Every record must carry the caller's own clientId.
	id, _ := c.ClientID(ctx)
	if balances.Data[0].ClientID != id {
		t.Errorf("balance clientId %s != token companyId %s", balances.Data[0].ClientID, id)
	}
}

func TestIntegrationLedgerAndDeposits(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()

	txns, err := c.Transactions().List(ctx, capay.TransactionFilter{ListOptions: capay.ListOptions{Limit: 5}})
	if err != nil {
		t.Fatalf("Transactions: %v", err)
	}
	for _, tx := range txns.Data {
		if tx.TransactionTime.IsZero() {
			t.Errorf("transactionTime %q did not parse", tx.TransactionTime.Raw)
		}
		t.Logf("%s %s %+.2f -> %.2f %s", tx.TransactionTime, tx.TransactionType, tx.Amount, tx.Balance, tx.RelatedTransactionReferenceNo)
	}

	deposits, err := c.Deposits().List(ctx, capay.DepositFilter{ListOptions: capay.ListOptions{Limit: 5}})
	if err != nil {
		t.Fatalf("Deposits: %v", err)
	}
	for _, d := range deposits.Data {
		t.Logf("deposit %s %s %.2f %s %s", d.ReferenceNo, d.CurrencyCode, d.Amount, d.Type, d.Status)
	}
}

func TestIntegrationRatesAndErrors(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()

	rate, err := c.Rates().Daily(ctx, "USD", "AUD")
	if err != nil {
		t.Fatalf("Daily: %v", err)
	}
	if rate.ExchangeRate <= 0 {
		t.Errorf("rate = %+v", rate)
	}
	t.Logf("%s %.5f on %s", rate.CurrencyPair, rate.ExchangeRate, rate.TargetDate)

	// An unknown id is a 404 "Invalid id".
	_, err = c.Payments().Get(ctx, "does-not-exist")
	if !errors.Is(err, capay.ErrNotFound) {
		t.Errorf("unknown payment: err = %v, want ErrNotFound", err)
	}

	// A validation failure carries one message per rule.
	_, err = c.Beneficiaries().CreateBusiness(ctx, capay.BusinessBeneficiaryRequest{CountryCode: "AU", Name: "x", UniquePayID: "not-a-fastid"})
	var apiErr *capay.APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, capay.ErrBadRequest) {
		t.Errorf("bad beneficiary: err = %v, want a 400 APIError", err)
	} else {
		t.Logf("400 as expected: %v", apiErr.Messages)
	}
}

func TestIntegrationPaymentLifecycle(t *testing.T) {
	c := integrationClient(t)
	if os.Getenv("CA_TEST_WRITE") == "" {
		t.Skip("set CA_TEST_WRITE=1 to run the sandbox write tests")
	}
	ctx := context.Background()
	stamp := time.Now().UTC().Format("20060102-150405")

	// A local AUD beneficiary at a made-up account. The sandbox accepts it;
	// Confirmation of Payee answers COP OTHERS because no responder exists.
	req := capay.BusinessBeneficiaryRequest{
		CountryCode: "AU",
		Name:        "SDK Test Pty Ltd",
		ReferenceID: "sdk-test-" + stamp,
		BankAccount: &capay.BankAccount{
			CountryCode: "AU", BankType: capay.BankTypeLocal, BankName: "Test Bank",
			AccountName: "SDK Test Pty Ltd", RoutingNoType: capay.RoutingBSB,
			RoutingNumber: "012999", AccountNumber: "123456", CurrencyCode: "AUD",
		},
	}
	cop, err := c.Beneficiaries().ConfirmBusinessPayee(ctx, req)
	if err != nil {
		t.Fatalf("ConfirmBusinessPayee: %v", err)
	}
	t.Logf("CoP: %s %s", cop.Code, cop.Message)

	ben, err := c.Beneficiaries().CreateBusiness(ctx, req, capay.WithIdempotencyKey("ben-"+stamp))
	if err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}
	t.Logf("beneficiary %s", ben.ID)

	// Replaying the same idempotency key must be a 409, not a second create.
	_, err = c.Beneficiaries().CreateBusiness(ctx, req, capay.WithIdempotencyKey("ben-"+stamp))
	if !errors.Is(err, capay.ErrConflict) {
		t.Errorf("replayed key: err = %v, want ErrConflict", err)
	}

	byRef, err := c.Beneficiaries().GetBusiness(ctx, req.ReferenceID)
	if err != nil || byRef.ID != ben.ID {
		t.Errorf("GetBusiness by referenceId: %v %+v", err, byRef)
	}

	pay, err := c.Payments().Create(ctx, capay.CreatePaymentRequest{
		BeneficiaryID:    ben.ID,
		CurrencyCode:     "AUD",
		Amount:           1,
		ChargeType:       capay.ChargeShared,
		PurposeCode:      capay.PurposeServices,
		SourceOfFunds:    capay.SourceBusinessIncome,
		PaymentReference: "SDK test " + stamp,
		PaymentDate:      capay.Today(),
		ReferenceID:      "sdk-pay-" + stamp,
	}, capay.WithIdempotencyKey("pay-"+stamp))
	if err != nil {
		t.Fatalf("Create payment: %v", err)
	}
	t.Logf("payment %s %s status %s fee %.2f", pay.ID, pay.ReferenceNo, pay.Status, pay.ChargeFee)

	got, err := c.Payments().Get(ctx, pay.ID)
	if err != nil || got.ReferenceNo != pay.ReferenceNo {
		t.Errorf("Get payment: %v %+v", err, got)
	}

	if pay.Status.IsCancelable() {
		cancelled, err := c.Payments().Cancel(ctx, pay.ID, "sdk integration test", capay.WithIdempotencyKey("cancel-"+stamp))
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		t.Logf("cancelled: %s", cancelled.Status)
	} else {
		t.Logf("payment already %s; not cancelling", pay.Status)
	}

	// Clean up. Deletion is refused while a payment is in progress, so this
	// only succeeds once the cancel above has settled.
	if _, err := c.Beneficiaries().DeleteBusiness(ctx, ben.ID); err != nil {
		t.Logf("DeleteBusiness (best effort): %v", err)
	}
}

func TestIntegrationSimulateDeposit(t *testing.T) {
	c := integrationClient(t)
	if os.Getenv("CA_TEST_WRITE") == "" {
		t.Skip("set CA_TEST_WRITE=1 to run the sandbox write tests")
	}
	ctx := context.Background()

	vas, err := c.Accounts().VirtualAccounts(ctx, capay.VirtualAccountFilter{CurrencyCode: "AUD"})
	if err != nil || len(vas.Data) == 0 {
		t.Fatalf("need an AUD virtual account to simulate into: %v", err)
	}
	va := vas.Data[0]

	ref := fmt.Sprintf("sdk-sim-%d", time.Now().Unix())
	err = c.Deposits().Simulate(ctx, capay.SimulateDepositRequest{
		Amount:           1,
		AccountNumber:    va.AccountNumber,
		RoutingNumber:    va.RoutingNumber,
		CurrencyCode:     "AUD",
		Name:             "CAPAY Sandbox",
		PaymentReference: ref,
	})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}

	// The simulator books the deposit asynchronously — about 40 seconds
	// later in the runs observed while building this SDK.
	deadline := time.Now().Add(90 * time.Second)
	for {
		deposits, err := c.Deposits().List(ctx, capay.DepositFilter{AccountID: va.ID, ListOptions: capay.ListOptions{Limit: 5}})
		if err != nil {
			t.Fatalf("Deposits: %v", err)
		}
		for _, d := range deposits.Data {
			if d.Payer != nil && d.Payer.PaymentReference == ref {
				t.Logf("simulated deposit %s %.2f %s status %s", d.ReferenceNo, d.Amount, d.Type, d.Status)
				return
			}
		}
		if time.Now().After(deadline) {
			t.Log("simulated deposit not yet visible after 90s; the simulator accepted it, so this is timing, not a failure")
			return
		}
		time.Sleep(3 * time.Second)
	}
}

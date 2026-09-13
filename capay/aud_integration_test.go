package capay_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/openlib-dev/corpalliance-sdk/capay"
)

// AUD use-case tests exercise the everyday flow of an Australian-dollar
// account against the sandbox:
//
//  1. check the AUD balance and list the AUD accounts (virtual accounts);
//  2. list AUD transactions from the ledger;
//  3. create an AUD transfer to a beneficiary.
//
// Steps 1 and 2 are read-only and run whenever credentials are present:
//
//	CA_API_USER=… CA_API_KEY=… go test ./capay/ -run AUD -v
//
// Step 3 writes to the sandbox (1 AUD to a throwaway beneficiary, then
// cancelled) and also needs CA_TEST_WRITE=1.
const audCurrency = "AUD"

// TestAUDBalanceAndAccounts — use case 1.
func TestAUDBalanceAndAccounts(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()

	t.Run("balance", func(t *testing.T) {
		balances, err := c.Accounts().Balances(ctx, capay.BalanceFilter{CurrencyCodes: []string{audCurrency}})
		if err != nil {
			t.Fatalf("Balances: %v", err)
		}
		if len(balances.Data) == 0 {
			t.Fatal("no AUD balance returned; the sandbox account should hold AUD")
		}
		for _, b := range balances.Data {
			if b.CurrencyCode != audCurrency {
				t.Errorf("balance %s is %s, filtered for %s", b.ID, b.CurrencyCode, audCurrency)
			}
			if b.Balance < 0 {
				t.Errorf("AUD balance is negative: %.2f", b.Balance)
			}
			t.Logf("AUD balance %.2f (client %s)", b.Balance, b.ClientID)
		}
	})

	t.Run("accounts", func(t *testing.T) {
		accounts, err := c.Accounts().VirtualAccounts(ctx, capay.VirtualAccountFilter{CurrencyCode: audCurrency})
		if err != nil {
			t.Fatalf("VirtualAccounts: %v", err)
		}
		if len(accounts.Data) == 0 {
			t.Fatal("no AUD accounts returned")
		}
		for _, a := range accounts.Data {
			if a.CurrencyCode != audCurrency {
				t.Errorf("account %s is %s, filtered for %s", a.ID, a.CurrencyCode, audCurrency)
			}
			if a.RoutingNoType != capay.RoutingBSB {
				t.Errorf("AUD account %s routing type = %s, want BSB", a.ID, a.RoutingNoType)
			}
			if a.RoutingNumber == "" || a.AccountNumber == "" {
				t.Errorf("AUD account %s is missing BSB/account number", a.ID)
			}
			t.Logf("AUD account %s BSB %s acct %s payid=%q %s", a.ID, a.RoutingNumber, a.AccountNumber, a.PayID, a.Status)
		}
	})
}

// TestAUDTransactions — use case 2.
func TestAUDTransactions(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()

	txns, err := c.Transactions().List(ctx, capay.TransactionFilter{
		CurrencyCode: audCurrency,
		ListOptions:  capay.ListOptions{Limit: 20},
	})
	if err != nil {
		t.Fatalf("Transactions: %v", err)
	}
	t.Logf("%d AUD transactions (total %d)", len(txns.Data), txns.Meta.TotalCount)
	if len(txns.Data) == 0 {
		t.Log("the ledger has no AUD transactions yet; run the write tests to create some")
		return
	}
	for _, tx := range txns.Data {
		if tx.CurrencyCode != audCurrency {
			t.Errorf("transaction %s is %s, filtered for %s", tx.RelatedTransactionReferenceNo, tx.CurrencyCode, audCurrency)
		}
		if tx.TransactionType == "" {
			t.Errorf("transaction %s has no type", tx.RelatedTransactionReferenceNo)
		}
		if tx.TransactionTime.IsZero() {
			t.Errorf("transactionTime %q did not parse", tx.TransactionTime.Raw)
		}
		t.Logf("%s %-12s %+10.2f -> %10.2f AUD %s", tx.TransactionTime, tx.TransactionType, tx.Amount, tx.Balance, tx.RelatedTransactionReferenceNo)
	}
}

// TestAUDTransferToBeneficiary — use case 3.
func TestAUDTransferToBeneficiary(t *testing.T) {
	c := integrationClient(t)
	if os.Getenv("CA_TEST_WRITE") == "" {
		t.Skip("set CA_TEST_WRITE=1 to run the sandbox write tests")
	}
	ctx := context.Background()
	stamp := time.Now().UTC().Format("20060102-150405")
	const amount = 1.00

	// Balance before, so we can confirm the transfer was booked against AUD.
	before, err := c.Accounts().Balances(ctx, capay.BalanceFilter{CurrencyCodes: []string{audCurrency}})
	if err != nil || len(before.Data) == 0 {
		t.Fatalf("AUD balance before transfer: %v", err)
	}
	if before.Data[0].Balance < amount {
		t.Skipf("AUD balance %.2f is below the %.2f test transfer", before.Data[0].Balance, amount)
	}

	// A local AUD beneficiary. The sandbox accepts a made-up BSB/account.
	benReq := capay.BusinessBeneficiaryRequest{
		CountryCode: "AU",
		Name:        "AUD Test Pty Ltd",
		ReferenceID: "aud-ben-" + stamp,
		BankAccount: &capay.BankAccount{
			CountryCode: "AU", BankType: capay.BankTypeLocal, BankName: "Test Bank",
			AccountName: "AUD Test Pty Ltd", RoutingNoType: capay.RoutingBSB,
			RoutingNumber: "012999", AccountNumber: "123456", CurrencyCode: audCurrency,
		},
	}
	ben, err := c.Beneficiaries().CreateBusiness(ctx, benReq, capay.WithIdempotencyKey("aud-ben-"+stamp))
	if err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}
	t.Logf("beneficiary %s", ben.ID)
	t.Cleanup(func() {
		if _, err := c.Beneficiaries().DeleteBusiness(ctx, ben.ID); err != nil {
			t.Logf("DeleteBusiness (best effort): %v", err)
		}
	})

	pay, err := c.Payments().Create(ctx, capay.CreatePaymentRequest{
		BeneficiaryID:    ben.ID,
		CurrencyCode:     audCurrency,
		Amount:           amount,
		ChargeType:       capay.ChargeShared,
		PurposeCode:      capay.PurposeServices,
		SourceOfFunds:    capay.SourceBusinessIncome, // required for local AUD payments
		PaymentReference: "AUD test " + stamp,
		PaymentDate:      capay.Today(),
		ReferenceID:      "aud-pay-" + stamp,
	}, capay.WithIdempotencyKey("aud-pay-"+stamp))
	if err != nil {
		t.Fatalf("Create payment: %v", err)
	}
	t.Logf("payment %s ref %s status %s fee %.2f", pay.ID, pay.ReferenceNo, pay.Status, pay.ChargeFee)

	// The transfer must be a same-currency AUD payment: no FX leg at all.
	if pay.CurrencyCode != audCurrency {
		t.Errorf("payment currency = %s, want %s", pay.CurrencyCode, audCurrency)
	}
	if pay.Amount != amount {
		t.Errorf("payment amount = %.2f, want %.2f", pay.Amount, amount)
	}
	if pay.BeneficiaryID != ben.ID {
		t.Errorf("payment beneficiary = %s, want %s", pay.BeneficiaryID, ben.ID)
	}
	if pay.SellCurrencyCode != "" || pay.QuotationID != "" || pay.ExchangeRate != 0 {
		t.Errorf("AUD->AUD payment carries an FX leg: sell=%s quotation=%s rate=%v", pay.SellCurrencyCode, pay.QuotationID, pay.ExchangeRate)
	}
	if pay.Status.IsFinal() && pay.Status != capay.PaymentCompleted {
		t.Errorf("payment ended as %s: %s", pay.Status, pay.FailureReason)
	}

	// Replaying the idempotency key is a 409, never a second transfer.
	_, err = c.Payments().Create(ctx, capay.CreatePaymentRequest{
		BeneficiaryID: ben.ID, CurrencyCode: audCurrency, Amount: amount,
		ChargeType: capay.ChargeShared, PurposeCode: capay.PurposeServices,
		SourceOfFunds: capay.SourceBusinessIncome, PaymentReference: "AUD test " + stamp,
		PaymentDate: capay.Today(), ReferenceID: "aud-pay-" + stamp,
	}, capay.WithIdempotencyKey("aud-pay-"+stamp))
	if !errors.Is(err, capay.ErrConflict) {
		t.Errorf("replayed idempotency key: err = %v, want ErrConflict", err)
	}

	// It shows up in the AUD payment list.
	list, err := c.Payments().List(ctx, capay.PaymentFilter{CurrencyCode: audCurrency, ListOptions: capay.ListOptions{Limit: 20}})
	if err != nil {
		t.Fatalf("List payments: %v", err)
	}
	found := false
	for _, p := range list.Data {
		if p.ID == pay.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("payment %s not in the AUD payment list", pay.ID)
	}

	// Don't leave a live transfer behind in the sandbox.
	if pay.Status.IsCancelable() {
		cancelled, err := c.Payments().Cancel(ctx, pay.ID, "aud use-case test", capay.WithIdempotencyKey("aud-cancel-"+stamp))
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		t.Logf("cancelled: %s", cancelled.Status)
	}
}

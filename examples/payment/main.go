// Command payment creates a local AUD beneficiary, checks it with
// Confirmation of Payee, pays it 1 AUD and then cancels the payment. Sandbox
// only.
//
//	CA_API_USER=… CA_API_KEY=… go run ./examples/payment
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/openlib-dev/corpalliance-sdk/capay"
)

func main() {
	user, key := os.Getenv("CA_API_USER"), os.Getenv("CA_API_KEY")
	if user == "" || key == "" {
		log.Fatal("set CA_API_USER and CA_API_KEY")
	}

	c := capay.New(user, key, capay.Sandbox)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A unique stamp doubles as the idempotency key prefix and referenceId,
	// so re-running the program never double-creates anything.
	stamp := time.Now().UTC().Format("20060102-150405")

	beneficiary := capay.BusinessBeneficiaryRequest{
		CountryCode: "AU",
		Name:        "Example Pty Ltd",
		ReferenceID: "example-ben-" + stamp,
		BankAccount: &capay.BankAccount{
			CountryCode:   "AU",
			BankType:      capay.BankTypeLocal,
			BankName:      "Example Bank",
			AccountName:   "Example Pty Ltd",
			RoutingNoType: capay.RoutingBSB,
			RoutingNumber: "012999",
			AccountNumber: "123456",
			CurrencyCode:  "AUD",
		},
	}

	// Confirmation of Payee before creating a local AUD beneficiary. In the
	// sandbox there is no responder behind the BSB, so this reports
	// COP OTHERS; in production a NOT MATCHED here is the moment to stop.
	cop, err := c.Beneficiaries().ConfirmBusinessPayee(ctx, beneficiary)
	if err != nil {
		fail(err)
	}
	fmt.Printf("CoP: %s (%s)\n", cop.Code, cop.Message)
	if cop.Code == capay.CoPNotMatched {
		log.Fatal("account name does not match; not paying")
	}

	ben, err := c.Beneficiaries().CreateBusiness(ctx, beneficiary, capay.WithIdempotencyKey("ben-"+stamp))
	if err != nil {
		fail(err)
	}
	fmt.Println("beneficiary:", ben.ID)

	payment, err := c.Payments().Create(ctx, capay.CreatePaymentRequest{
		BeneficiaryID:    ben.ID,
		CurrencyCode:     "AUD",
		Amount:           1,
		ChargeType:       capay.ChargeShared,
		PurposeCode:      capay.PurposeServices,
		SourceOfFunds:    capay.SourceBusinessIncome, // required for local payments
		PaymentReference: "Example " + stamp,
		PaymentDate:      capay.Today(),
		ReferenceID:      "example-pay-" + stamp,
	}, capay.WithIdempotencyKey("pay-"+stamp))
	if err != nil {
		fail(err)
	}
	fmt.Printf("payment: %s %s status %s fee %.2f\n", payment.ID, payment.ReferenceNo, payment.Status, payment.ChargeFee)

	if payment.Status.IsCancelable() {
		cancelled, err := c.Payments().Cancel(ctx, payment.ID, "example run", capay.WithIdempotencyKey("cancel-"+stamp))
		if err != nil {
			fail(err)
		}
		fmt.Println("cancelled:", cancelled.Status)
	}

	if _, err := c.Beneficiaries().DeleteBusiness(ctx, ben.ID); err != nil {
		fmt.Println("beneficiary not deleted (a payment may still be in progress):", err)
	}
}

func fail(err error) {
	var apiErr *capay.APIError
	if errors.As(err, &apiErr) {
		log.Fatalf("%s %s -> %d: %v", apiErr.Method, apiErr.Endpoint, apiErr.StatusCode, apiErr.Messages)
	}
	log.Fatal(err)
}

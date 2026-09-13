// Command basic authenticates against the sandbox and prints balances,
// virtual accounts and the most recent ledger entries.
//
//	CA_API_USER=… CA_API_KEY=… go run ./examples/basic
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id, err := c.ClientID(ctx)
	if err != nil {
		fail(err)
	}
	fmt.Println("client:", id)

	balances, err := c.Accounts().Balances(ctx, capay.BalanceFilter{})
	if err != nil {
		fail(err)
	}
	for _, b := range balances.Data {
		fmt.Printf("balance  %s %.2f\n", b.CurrencyCode, b.Balance)
	}

	vas, err := c.Accounts().VirtualAccounts(ctx, capay.VirtualAccountFilter{})
	if err != nil {
		fail(err)
	}
	for _, va := range vas.Data {
		fmt.Printf("virtual  %s %s %s %s  acct %s  payid %s  %s\n",
			va.CurrencyCode, va.AccountName, va.RoutingNoType, va.RoutingNumber, va.AccountNumber, va.PayID, va.Status)
	}

	txns, err := c.Transactions().List(ctx, capay.TransactionFilter{ListOptions: capay.ListOptions{Limit: 10}})
	if err != nil {
		fail(err)
	}
	for _, tx := range txns.Data {
		fmt.Printf("ledger   %s %-12s %+12.2f  -> %12.2f  %s\n",
			tx.TransactionTime.Format("2006-01-02 15:04"), tx.TransactionType, tx.Amount, tx.Balance, tx.RelatedTransactionReferenceNo)
	}
	fmt.Printf("%d of %d ledger entries shown\n", len(txns.Data), txns.Meta.TotalCount)
}

// fail prints an error with as much of the API's own reason as it carried.
func fail(err error) {
	var apiErr *capay.APIError
	if errors.As(err, &apiErr) {
		log.Fatalf("%s %s -> %d: %v", apiErr.Method, apiErr.Endpoint, apiErr.StatusCode, apiErr.Messages)
	}
	log.Fatal(err)
}

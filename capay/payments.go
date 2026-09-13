package capay

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// PaymentsService initiates payments to beneficiaries and manages them through
// their lifecycle, and locks FX quotations for cross-currency payments.
//
// Which account funds a payment is decided by ClientID: omit it and the money
// leaves your House Account under your name; supply a customer's clientId and
// the payment is made on that customer's behalf (POBO) — the system allocates
// from your House Account to the customer's Sub-Account and pays out from
// there, producing a TRANSFER and a PAYMENT in the ledger.
//
// Always pass WithIdempotencyKey when creating a payment.
type PaymentsService interface {
	// Create sends a payment to an existing beneficiary.
	//
	// For a cross-currency payment set SellCurrencyCode and a QuotationID
	// from CreateQuotation; the API answers HTTP 201.
	Create(ctx context.Context, req CreatePaymentRequest, opts ...RequestOption) (*Payment, error)

	// CreateWithBeneficiary creates the beneficiary and the payment in one
	// call. If a matching beneficiary already exists it is reused (matching
	// on clientId + routing number + account number + account name +
	// currency code); otherwise one is created.
	CreateWithBeneficiary(ctx context.Context, req CreatePaymentWithBeneficiaryRequest, opts ...RequestOption) (*Payment, error)

	// CreateQuotation locks a firm FX rate. Pass the returned QuotationID to
	// Create before ExpiryTime; an expired quotation cannot be used.
	CreateQuotation(ctx context.Context, req QuotationRequest, opts ...RequestOption) (*Quotation, error)

	// List returns payments matching the filter.
	List(ctx context.Context, filter PaymentFilter) (*Page[Payment], error)

	// Get fetches one payment by id or by your referenceId.
	Get(ctx context.Context, idOrReferenceID string) (*Payment, error)

	// Update amends a payment's reference, date, purpose, source of funds or
	// invoice details while it is still in progress. It is a PUT — send every
	// required field, not only the one that changes.
	Update(ctx context.Context, id string, req UpdatePaymentRequest, opts ...RequestOption) (*Payment, error)

	// Cancel cancels a payment. Only possible while the status is
	// PENDING APPROVAL, SCHEDULED or WAITING FUNDS; once SENDING it cannot
	// be cancelled. Funds already deducted are refunded automatically.
	Cancel(ctx context.Context, id, reason string, opts ...RequestOption) (*Payment, error)
}

// paymentsService implements PaymentsService.
type paymentsService struct{ c *client }

// ChargeType decides who bears SWIFT and intermediary fees. It applies to
// SWIFT payments only; local and internal payments ignore it, but the field
// is still required.
type ChargeType string

const (
	// ChargeShared — fees are shared, deducted from both the payment amount
	// and the payment fee.
	ChargeShared ChargeType = "SHARED"

	// ChargeOurs — you bear all fees, deducted from the payment fee only.
	ChargeOurs ChargeType = "OURS"
)

// PurposeCode describes the nature of a payment.
type PurposeCode string

const (
	PurposeGoods              PurposeCode = "GOODS"
	PurposeServices           PurposeCode = "SERVICES"
	PurposeSaving             PurposeCode = "SAVING"
	PurposeFamily             PurposeCode = "FAMILY"
	PurposeInterGroupTransfer PurposeCode = "INTER_GROUP_TRANSFER"
	PurposeHighValueItem      PurposeCode = "HIGH_VALUE_ITEM"
	PurposeInvestment         PurposeCode = "INVESTMENT"
	PurposeDebt               PurposeCode = "DEBT"
	PurposeCompanyManage      PurposeCode = "COMPANY_MANAGE"
	PurposeFreight            PurposeCode = "FREIGHT"
)

// SourceOfFunds states where the money comes from. Required for local
// payments and for POBO SWIFT payments to personal beneficiaries.
type SourceOfFunds string

const (
	SourceSalary         SourceOfFunds = "Salary"
	SourceSavings        SourceOfFunds = "Savings"
	SourceLoan           SourceOfFunds = "Loan"
	SourceBusinessIncome SourceOfFunds = "Business Income"
)

// PaymentStatus is a payment's lifecycle state. Every change fires a PAYMENT
// webhook.
type PaymentStatus string

const (
	// PaymentPendingApproval — awaiting approval inside your own
	// organisation under the limits configured in the Client Portal. This is
	// not a Corporate Alliance review. Cancelable.
	PaymentPendingApproval PaymentStatus = "PENDING APPROVAL"

	// PaymentScheduled — approved and queued for the payment date. Cancelable.
	PaymentScheduled PaymentStatus = "SCHEDULED"

	// PaymentWaitingFunds — needs funding before it can be sent. Cancelable.
	PaymentWaitingFunds PaymentStatus = "WAITING FUNDS"

	// PaymentSending — in transit through the payment network. Not cancelable.
	PaymentSending PaymentStatus = "SENDING"

	// PaymentCompleted — final: funds delivered.
	PaymentCompleted PaymentStatus = "COMPLETED"

	// PaymentCancelled — final: cancelled before sending, auto-refunded.
	PaymentCancelled PaymentStatus = "CANCELLED"

	// PaymentFailed — final: could not be completed, auto-refunded. See
	// Payment.FailureReason.
	PaymentFailed PaymentStatus = "FAILED"

	// PaymentRejected — final: did not pass internal approval.
	PaymentRejected PaymentStatus = "REJECTED"
)

// IsFinal reports whether the status is terminal.
func (s PaymentStatus) IsFinal() bool {
	switch s {
	case PaymentCompleted, PaymentCancelled, PaymentFailed, PaymentRejected:
		return true
	}
	return false
}

// IsCancelable reports whether Cancel can still succeed from this status.
func (s PaymentStatus) IsCancelable() bool {
	switch s {
	case PaymentPendingApproval, PaymentScheduled, PaymentWaitingFunds:
		return true
	}
	return false
}

// paymentFields are the fields common to every payment request, gathered
// for local validation. The request structs repeat them flat so callers can
// build one with a plain composite literal.
type paymentFields struct {
	// ClientID makes the payment on behalf of one of your customers (POBO).
	// Required when the beneficiary belongs to that customer; omit to pay
	// from your own House Account.
	ClientID string `json:"clientId,omitempty"`

	// CurrencyCode is the payment currency — what the beneficiary receives.
	CurrencyCode string `json:"currencyCode"`

	// SellCurrencyCode is the currency you hold, when it differs from
	// CurrencyCode. Requires QuotationID.
	SellCurrencyCode string `json:"sellCurrencyCode,omitempty"`

	// QuotationID is a locked rate from CreateQuotation, for FX payments.
	QuotationID string `json:"quotationId,omitempty"`

	// Amount is in the payment currency, at most two decimal places, and at
	// least 1.
	Amount float64 `json:"amount"`

	// ChargeType is SHARED or OURS. Required even for non-SWIFT payments.
	ChargeType ChargeType `json:"chargeType"`

	// PaymentReference is shown to the beneficiary (≤ 64 characters of
	// letters, digits, spaces and / - ? : ( ) . , ' +).
	PaymentReference string `json:"paymentReference"`

	// PaymentDate is the date to execute on.
	PaymentDate Date `json:"paymentDate"`

	// PurposeCode is the reason for the payment.
	PurposeCode PurposeCode `json:"purposeCode"`

	// SourceOfFunds is conditionally required; see the type.
	SourceOfFunds SourceOfFunds `json:"sourceOfFunds,omitempty"`

	// InvoiceNumber and InvoiceDate are required for local INR payments to
	// business beneficiaries.
	InvoiceNumber string `json:"invoiceNumber,omitempty"`
	InvoiceDate   Date   `json:"invoiceDate,omitzero"`

	// ReferenceID is your own unique reference (≤ 36 characters). Creation
	// fails if a payment with the same ReferenceID exists, and the payment
	// can later be fetched by it. Distinct from the transport-level
	// idempotency key, and worth setting alongside it.
	ReferenceID string `json:"referenceId,omitempty"`
}

// paymentLike is implemented by every request that carries paymentFields.
type paymentLike interface{ core() paymentFields }

// validate applies the rules the API states, so an obviously bad payment
// fails in Go rather than as a 400.
func validatePayment(r paymentLike) error {
	p := r.core()
	switch {
	case p.CurrencyCode == "":
		return invalidInput("CurrencyCode is required")
	case p.Amount < 1:
		return invalidInput("Amount must be at least 1, got %v", p.Amount)
	case math.Round(p.Amount*100) != p.Amount*100:
		return invalidInput("Amount %v has more than two decimal places", p.Amount)
	case p.ChargeType == "":
		return invalidInput("ChargeType is required (SHARED or OURS)")
	case p.PaymentReference == "":
		return invalidInput("PaymentReference is required")
	case len(p.PaymentReference) > 64:
		return invalidInput("PaymentReference is longer than 64 characters")
	case p.PaymentDate.IsZero():
		return invalidInput("PaymentDate is required")
	case p.PurposeCode == "":
		return invalidInput("PurposeCode is required")
	case (p.SellCurrencyCode != "") != (p.QuotationID != ""):
		return invalidInput("SellCurrencyCode and QuotationID must be set together for an FX payment")
	}
	return nil
}

// CreatePaymentRequest pays an existing beneficiary.
type CreatePaymentRequest struct {
	// ClientID makes the payment on behalf of one of your customers (POBO).
	// Required when the beneficiary belongs to that customer; omit to pay
	// from your own House Account.
	ClientID string `json:"clientId,omitempty"`

	// CurrencyCode is the payment currency — what the beneficiary receives.
	CurrencyCode string `json:"currencyCode"`

	// SellCurrencyCode is the currency you hold, when it differs from
	// CurrencyCode. Requires QuotationID.
	SellCurrencyCode string `json:"sellCurrencyCode,omitempty"`

	// QuotationID is a locked rate from CreateQuotation, for FX payments.
	QuotationID string `json:"quotationId,omitempty"`

	// Amount is in the payment currency, at most two decimal places, and at
	// least 1.
	Amount float64 `json:"amount"`

	// ChargeType is SHARED or OURS. Required even for non-SWIFT payments.
	ChargeType ChargeType `json:"chargeType"`

	// PaymentReference is shown to the beneficiary (≤ 64 characters of
	// letters, digits, spaces and / - ? : ( ) . , ' +).
	PaymentReference string `json:"paymentReference"`

	// PaymentDate is the date to execute on.
	PaymentDate Date `json:"paymentDate"`

	// PurposeCode is the reason for the payment.
	PurposeCode PurposeCode `json:"purposeCode"`

	// SourceOfFunds is conditionally required; see the type.
	SourceOfFunds SourceOfFunds `json:"sourceOfFunds,omitempty"`

	// InvoiceNumber and InvoiceDate are required for local INR payments to
	// business beneficiaries.
	InvoiceNumber string `json:"invoiceNumber,omitempty"`
	InvoiceDate   Date   `json:"invoiceDate,omitzero"`

	// ReferenceID is your own unique reference (≤ 36 characters). Creation
	// fails if a payment with the same ReferenceID exists, and the payment
	// can later be fetched by it. Distinct from the transport-level
	// idempotency key, and worth setting alongside it.
	ReferenceID string `json:"referenceId,omitempty"`

	// BeneficiaryID is the beneficiary to pay.
	BeneficiaryID string `json:"beneficiaryId"`
}

func (r CreatePaymentRequest) core() paymentFields {
	return paymentFields{
		ClientID:         r.ClientID,
		CurrencyCode:     r.CurrencyCode,
		SellCurrencyCode: r.SellCurrencyCode,
		QuotationID:      r.QuotationID,
		Amount:           r.Amount,
		ChargeType:       r.ChargeType,
		PaymentReference: r.PaymentReference,
		PaymentDate:      r.PaymentDate,
		PurposeCode:      r.PurposeCode,
		SourceOfFunds:    r.SourceOfFunds,
		InvoiceNumber:    r.InvoiceNumber,
		InvoiceDate:      r.InvoiceDate,
		ReferenceID:      r.ReferenceID,
	}
}

// BeneficiaryType selects which beneficiary shape an inline payment carries.
type BeneficiaryType string

const (
	BeneficiaryBusiness BeneficiaryType = "BUSINESS"
	BeneficiaryPersonal BeneficiaryType = "PERSONAL"
)

// CreatePaymentWithBeneficiaryRequest pays a beneficiary described inline,
// creating it first if it does not already exist.
//
// Exactly one of Business or Personal must be set; BeneficiaryType is derived
// from which. The beneficiary's currency code and client id are inherited from
// the payment and may be left empty.
type CreatePaymentWithBeneficiaryRequest struct {
	// ClientID makes the payment on behalf of one of your customers (POBO).
	// Required when the beneficiary belongs to that customer; omit to pay
	// from your own House Account.
	ClientID string `json:"clientId,omitempty"`

	// CurrencyCode is the payment currency — what the beneficiary receives.
	CurrencyCode string `json:"currencyCode"`

	// SellCurrencyCode is the currency you hold, when it differs from
	// CurrencyCode. Requires QuotationID.
	SellCurrencyCode string `json:"sellCurrencyCode,omitempty"`

	// QuotationID is a locked rate from CreateQuotation, for FX payments.
	QuotationID string `json:"quotationId,omitempty"`

	// Amount is in the payment currency, at most two decimal places, and at
	// least 1.
	Amount float64 `json:"amount"`

	// ChargeType is SHARED or OURS. Required even for non-SWIFT payments.
	ChargeType ChargeType `json:"chargeType"`

	// PaymentReference is shown to the beneficiary (≤ 64 characters of
	// letters, digits, spaces and / - ? : ( ) . , ' +).
	PaymentReference string `json:"paymentReference"`

	// PaymentDate is the date to execute on.
	PaymentDate Date `json:"paymentDate"`

	// PurposeCode is the reason for the payment.
	PurposeCode PurposeCode `json:"purposeCode"`

	// SourceOfFunds is conditionally required; see the type.
	SourceOfFunds SourceOfFunds `json:"sourceOfFunds,omitempty"`

	// InvoiceNumber and InvoiceDate are required for local INR payments to
	// business beneficiaries.
	InvoiceNumber string `json:"invoiceNumber,omitempty"`
	InvoiceDate   Date   `json:"invoiceDate,omitzero"`

	// ReferenceID is your own unique reference (≤ 36 characters). Creation
	// fails if a payment with the same ReferenceID exists, and the payment
	// can later be fetched by it. Distinct from the transport-level
	// idempotency key, and worth setting alongside it.
	ReferenceID string `json:"referenceId,omitempty"`

	// Business describes a business beneficiary.
	Business *BusinessBeneficiaryRequest `json:"-"`

	// Personal describes a personal beneficiary.
	Personal *PersonalBeneficiaryRequest `json:"-"`
}

func (r CreatePaymentWithBeneficiaryRequest) core() paymentFields {
	return paymentFields{
		ClientID:         r.ClientID,
		CurrencyCode:     r.CurrencyCode,
		SellCurrencyCode: r.SellCurrencyCode,
		QuotationID:      r.QuotationID,
		Amount:           r.Amount,
		ChargeType:       r.ChargeType,
		PaymentReference: r.PaymentReference,
		PaymentDate:      r.PaymentDate,
		PurposeCode:      r.PurposeCode,
		SourceOfFunds:    r.SourceOfFunds,
		InvoiceNumber:    r.InvoiceNumber,
		InvoiceDate:      r.InvoiceDate,
		ReferenceID:      r.ReferenceID,
	}
}

// MarshalJSON adds beneficiaryType and beneficiary to the payment fields.
func (r CreatePaymentWithBeneficiaryRequest) MarshalJSON() ([]byte, error) {
	type wire struct {
		paymentFields
		BeneficiaryType BeneficiaryType `json:"beneficiaryType"`
		Beneficiary     any             `json:"beneficiary"`
	}
	w := wire{paymentFields: r.core()}
	switch {
	case r.Business != nil:
		w.BeneficiaryType, w.Beneficiary = BeneficiaryBusiness, r.Business
	case r.Personal != nil:
		w.BeneficiaryType, w.Beneficiary = BeneficiaryPersonal, r.Personal
	}
	return json.Marshal(w)
}

// Payment is a payment as the API returns it.
type Payment struct {
	// ClientID makes the payment on behalf of one of your customers (POBO).
	// Required when the beneficiary belongs to that customer; omit to pay
	// from your own House Account.
	ClientID string `json:"clientId,omitempty"`

	// CurrencyCode is the payment currency — what the beneficiary receives.
	CurrencyCode string `json:"currencyCode"`

	// SellCurrencyCode is the currency you hold, when it differs from
	// CurrencyCode. Requires QuotationID.
	SellCurrencyCode string `json:"sellCurrencyCode,omitempty"`

	// QuotationID is a locked rate from CreateQuotation, for FX payments.
	QuotationID string `json:"quotationId,omitempty"`

	// Amount is in the payment currency, at most two decimal places, and at
	// least 1.
	Amount float64 `json:"amount"`

	// ChargeType is SHARED or OURS. Required even for non-SWIFT payments.
	ChargeType ChargeType `json:"chargeType"`

	// PaymentReference is shown to the beneficiary (≤ 64 characters of
	// letters, digits, spaces and / - ? : ( ) . , ' +).
	PaymentReference string `json:"paymentReference"`

	// PaymentDate is the date to execute on.
	PaymentDate Date `json:"paymentDate"`

	// PurposeCode is the reason for the payment.
	PurposeCode PurposeCode `json:"purposeCode"`

	// SourceOfFunds is conditionally required; see the type.
	SourceOfFunds SourceOfFunds `json:"sourceOfFunds,omitempty"`

	// InvoiceNumber and InvoiceDate are required for local INR payments to
	// business beneficiaries.
	InvoiceNumber string `json:"invoiceNumber,omitempty"`
	InvoiceDate   Date   `json:"invoiceDate,omitzero"`

	// ReferenceID is your own unique reference (≤ 36 characters). Creation
	// fails if a payment with the same ReferenceID exists, and the payment
	// can later be fetched by it. Distinct from the transport-level
	// idempotency key, and worth setting alongside it.
	ReferenceID string `json:"referenceId,omitempty"`

	// BeneficiaryID is the beneficiary paid.
	BeneficiaryID string `json:"beneficiaryId"`

	// ID is the payment's id.
	ID string `json:"id"`

	// ReferenceNo is the human-readable reference, e.g. "20240726-PA8JZ4".
	// It is what a webhook and a ledger line quote.
	ReferenceNo string `json:"referenceNo"`

	// ChargeFee is the fee, in the payment currency.
	ChargeFee float64 `json:"chargeFee"`

	// SellAmount, ExchangeRate and CurrencyPair are populated on FX payments.
	SellAmount   float64 `json:"sellAmount,omitempty"`
	ExchangeRate float64 `json:"exchangeRate,omitempty"`
	CurrencyPair string  `json:"currencyPair,omitempty"`

	// Status is the lifecycle state.
	Status PaymentStatus `json:"status"`

	// FailureReason explains a FAILED payment.
	FailureReason string `json:"failureReason"`

	// CreatedTime and UpdatedTime are the record's timestamps.
	CreatedTime Time `json:"createdTime"`
	UpdatedTime Time `json:"updatedTime"`
}

// UpdatePaymentRequest amends a payment in progress. Every field except the
// invoice pair is required by the API.
type UpdatePaymentRequest struct {
	PaymentReference string        `json:"paymentReference"`
	PaymentDate      Date          `json:"paymentDate"`
	PurposeCode      PurposeCode   `json:"purposeCode"`
	SourceOfFunds    SourceOfFunds `json:"sourceOfFunds,omitempty"`
	InvoiceNumber    string        `json:"invoiceNumber,omitempty"`
	InvoiceDate      Date          `json:"invoiceDate,omitzero"`
}

// cancelRequest is the body of a cancel call.
type cancelRequest struct {
	Reason string `json:"reason,omitempty"`
}

// PaymentFilter narrows a List call.
type PaymentFilter struct {
	ListOptions

	// ReferenceNo matches the human-readable reference exactly.
	ReferenceNo string

	// CurrencyCode, ClientID, BeneficiaryID and Status restrict by field.
	CurrencyCode  string
	ClientID      string
	BeneficiaryID string
	Status        PaymentStatus

	// PaymentDateFrom/To bound the payment date, inclusive.
	PaymentDateFrom Date
	PaymentDateTo   Date

	// CreatedFrom/To and UpdatedFrom/To bound the record timestamps.
	CreatedFrom time.Time
	CreatedTo   time.Time
	UpdatedFrom time.Time
	UpdatedTo   time.Time
}

// FixedSide names which side of an FX quotation the amount fixes.
type FixedSide string

const (
	FixedSideBuy  FixedSide = "buy"
	FixedSideSell FixedSide = "sell"
)

// QuotationRequest locks an FX rate.
type QuotationRequest struct {
	// BaseCurrencyCode is the buy currency; QuoteCurrencyCode the sell.
	BaseCurrencyCode  string `json:"baseCurrencyCode"`
	QuoteCurrencyCode string `json:"quoteCurrencyCode"`

	// Date is the settlement date. Required.
	Date Date `json:"date"`

	// FixedSide and Amount optionally fix one side's amount.
	FixedSide FixedSide `json:"fixedSide,omitempty"`
	Amount    float64   `json:"amount,omitempty"`

	// ClientID attributes the quotation to a customer.
	ClientID string `json:"clientId,omitempty"`
}

// Quotation is a locked FX rate.
type Quotation struct {
	// QuotationID is the value to pass as CreatePaymentRequest.QuotationID.
	QuotationID string `json:"quotationId"`

	// CurrencyPair, BaseCurrencyCode and QuoteCurrencyCode identify the pair.
	CurrencyPair      string `json:"currencyPair"`
	BaseCurrencyCode  string `json:"baseCurrencyCode"`
	QuoteCurrencyCode string `json:"quoteCurrencyCode"`

	// ExchangeRate is the locked rate.
	ExchangeRate float64 `json:"exchangeRate"`

	// TargetDate is the settlement date.
	TargetDate Date `json:"targetDate"`

	// ExpiryTime is when the quotation stops being usable.
	ExpiryTime Time `json:"expiryTime"`
}

// Create implements PaymentsService.
func (s *paymentsService) Create(ctx context.Context, req CreatePaymentRequest, opts ...RequestOption) (*Payment, error) {
	if err := validatePayment(req); err != nil {
		return nil, err
	}
	if req.BeneficiaryID == "" {
		return nil, invalidInput("BeneficiaryID is required")
	}
	var out Payment
	if err := s.c.mutate(ctx, apiPaymentCreate, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWithBeneficiary implements PaymentsService.
func (s *paymentsService) CreateWithBeneficiary(ctx context.Context, req CreatePaymentWithBeneficiaryRequest, opts ...RequestOption) (*Payment, error) {
	if err := validatePayment(req); err != nil {
		return nil, err
	}
	switch {
	case req.Business == nil && req.Personal == nil:
		return nil, invalidInput("one of Business or Personal beneficiary is required")
	case req.Business != nil && req.Personal != nil:
		return nil, invalidInput("only one of Business or Personal beneficiary may be set")
	case req.Business != nil:
		if err := validateBeneficiary(*req.Business); err != nil {
			return nil, fmt.Errorf("%w (beneficiary)", err)
		}
		if req.Business.Name == "" {
			return nil, invalidInput("beneficiary Name is required")
		}
	case req.Personal != nil:
		if err := validateBeneficiary(*req.Personal); err != nil {
			return nil, fmt.Errorf("%w (beneficiary)", err)
		}
		if req.Personal.FirstName == "" || req.Personal.LastName == "" {
			return nil, invalidInput("beneficiary FirstName and LastName are required")
		}
	}
	var out Payment
	if err := s.c.mutate(ctx, apiPaymentCreateWithBenef, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateQuotation implements PaymentsService.
func (s *paymentsService) CreateQuotation(ctx context.Context, req QuotationRequest, opts ...RequestOption) (*Quotation, error) {
	switch {
	case req.BaseCurrencyCode == "" || req.QuoteCurrencyCode == "":
		return nil, invalidInput("BaseCurrencyCode and QuoteCurrencyCode are required")
	case req.Date.IsZero():
		return nil, invalidInput("Date is required")
	case (req.FixedSide != "") != (req.Amount != 0):
		return nil, invalidInput("FixedSide and Amount must be set together")
	}
	var out Quotation
	if err := s.c.mutate(ctx, apiPaymentQuotation, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// List implements PaymentsService.
func (s *paymentsService) List(ctx context.Context, filter PaymentFilter) (*Page[Payment], error) {
	q := newQuery().
		page(filter.ListOptions).
		str("referenceNo", filter.ReferenceNo).
		str("currencyCode", filter.CurrencyCode).
		str("clientId", filter.ClientID).
		str("beneficiaryId", filter.BeneficiaryID).
		str("status", string(filter.Status)).
		date("paymentDateFrom", filter.PaymentDateFrom).
		date("paymentDateTo", filter.PaymentDateTo).
		time("createdTimeFrom", filter.CreatedFrom).
		time("createdTimeTo", filter.CreatedTo).
		time("updatedTimeFrom", filter.UpdatedFrom).
		time("updatedTimeTo", filter.UpdatedTo)

	var out Page[Payment]
	if err := s.c.do(ctx, requestSpec{api: apiPaymentList, query: q.values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get implements PaymentsService.
func (s *paymentsService) Get(ctx context.Context, idOrReferenceID string) (*Payment, error) {
	if idOrReferenceID == "" {
		return nil, invalidInput("payment id is required")
	}
	var out Payment
	if err := s.c.do(ctx, requestSpec{api: apiPaymentGet, pathParams: idParam(idOrReferenceID), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update implements PaymentsService.
func (s *paymentsService) Update(ctx context.Context, id string, req UpdatePaymentRequest, opts ...RequestOption) (*Payment, error) {
	switch {
	case id == "":
		return nil, invalidInput("payment id is required")
	case req.PaymentReference == "":
		return nil, invalidInput("PaymentReference is required")
	case req.PaymentDate.IsZero():
		return nil, invalidInput("PaymentDate is required")
	case req.PurposeCode == "":
		return nil, invalidInput("PurposeCode is required")
	}
	var out Payment
	if err := s.c.mutate(ctx, apiPaymentUpdate, idParam(id), req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cancel implements PaymentsService.
func (s *paymentsService) Cancel(ctx context.Context, id, reason string, opts ...RequestOption) (*Payment, error) {
	if id == "" {
		return nil, invalidInput("payment id is required")
	}
	if len(reason) > 64 {
		return nil, invalidInput("cancel reason is longer than 64 characters")
	}
	var out Payment
	if err := s.c.mutate(ctx, apiPaymentCancel, idParam(id), cancelRequest{Reason: reason}, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

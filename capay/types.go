package capay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Wire formats.
const (
	// dateLayout is the YYYY-MM-DD form every date field uses, both ways.
	dateLayout = "2006-01-02"

	// timestampLayout is the ISO 8601 form the API documents for timestamps
	// and time-range query parameters.
	timestampLayout = "2006-01-02T15:04:05.000Z07:00"
)

// Date is a calendar date with no time or zone, the YYYY-MM-DD the API uses
// for paymentDate, depositDate, invoiceDate, dateOfBirth and the like.
//
// The zero Date marshals to JSON null and is omitted from query strings, so an
// unset optional date is never sent as "0001-01-01".
type Date struct{ time.Time }

// NewDate builds a Date from a year, month and day.
func NewDate(year int, month time.Month, day int) Date {
	return Date{time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// DateOf truncates t to its calendar date, in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return NewDate(y, m, d)
}

// Today returns the current date in UTC.
func Today() Date { return DateOf(time.Now().UTC()) }

// ParseDate parses a YYYY-MM-DD string.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, err
	}
	return Date{t}, nil
}

// String formats the date as YYYY-MM-DD, or "" for the zero Date.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.Format(dateLayout)
}

// MarshalJSON emits "YYYY-MM-DD", or null for the zero Date.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON accepts "YYYY-MM-DD", a full timestamp (whose date part is
// kept), an empty string or null.
func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*d = Date{}
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	if t, err := time.Parse(dateLayout, str); err == nil {
		*d = Date{t}
		return nil
	}
	var ts Time
	if err := ts.UnmarshalJSON(b); err != nil || ts.IsZero() {
		return fmt.Errorf("capay: %q is not a YYYY-MM-DD date", str)
	}
	*d = DateOf(ts.Time)
	return nil
}

// Time is a timestamp that decodes from every layout the API has been seen to
// use.
//
// The documentation says ISO 8601 throughout, and createdTime, updatedTime
// and timestamp fields do arrive as "2026-09-11T05:59:48.000Z". The ledger's
// transactionTime, however, arrives as "2026-09-11 05:59:48" — no T, no
// fraction, no zone. A strict time.Time field would fail the entire response
// decode over that one field, so Time tries each known layout in turn and
// keeps the original string in Raw for anything it could not parse.
//
// Zone-less values are interpreted as UTC, which matches the zone every other
// timestamp from the same service carries.
type Time struct {
	time.Time

	// Raw is the string exactly as received. It is populated on every decode,
	// so a value the SDK could not parse is still reachable.
	Raw string
}

// timeLayouts are tried in order. RFC3339Nano covers the documented form.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.000Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	dateLayout,
}

// UnmarshalJSON accepts any of the known layouts, an empty string or null.
// An unrecognised layout is not an error: Time is zero and Raw carries it.
func (t *Time) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*t = Time{}
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	*t = Time{Raw: str}
	str = strings.TrimSpace(str)
	if str == "" {
		return nil
	}
	for _, layout := range timeLayouts {
		if parsed, err := time.ParseInLocation(layout, str, time.UTC); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return nil
}

// MarshalJSON emits RFC 3339 with millisecond precision in UTC, or null for
// the zero Time.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.UTC().Format(timestampLayout))
}

// String implements fmt.Stringer.
func (t Time) String() string {
	if t.IsZero() {
		return t.Raw
	}
	return t.UTC().Format(timestampLayout)
}

// ListOptions paginates a list call. The API defaults are skip 0, limit 10.
type ListOptions struct {
	// Skip is the number of records to skip.
	Skip int

	// Limit is the maximum number of records to return. Zero leaves the API
	// default (10) in force.
	Limit int
}

// Meta describes a page of results.
type Meta struct {
	// Skip and Limit echo the pagination that produced this page.
	Skip  int `json:"skip"`
	Limit int `json:"limit"`

	// TotalCount is the number of records matching the query across all
	// pages. Use it to drive pagination.
	TotalCount int `json:"totalCount"`

	// Timestamp is when the query ran.
	Timestamp Time `json:"timestamp"`
}

// Page is one page of a list response: the records plus the metadata needed to
// fetch the next page.
type Page[T any] struct {
	Meta Meta `json:"meta"`
	Data []T  `json:"data"`
}

// HasMore reports whether records remain beyond this page.
func (p Page[T]) HasMore() bool { return p.Meta.Skip+len(p.Data) < p.Meta.TotalCount }

// NextSkip returns the Skip to request the following page with.
func (p Page[T]) NextSkip() int { return p.Meta.Skip + len(p.Data) }

// Address is a postal address, used for beneficiaries, customers and banks.
//
// Allowed characters per field, as the API enforces them: street — letters,
// digits, spaces and , . - / # ( ) : ' ; city — letters, spaces and - . ' ;
// state — letters, spaces and - ; zip — letters, digits, hyphens and spaces.
type Address struct {
	Street string `json:"street,omitempty"`
	City   string `json:"city,omitempty"`
	State  string `json:"state,omitempty"`
	Zip    string `json:"zip,omitempty"`
}

// BankType distinguishes a SWIFT-addressed bank from one on local rails.
type BankType string

const (
	BankTypeSWIFT BankType = "SWIFT"
	BankTypeLocal BankType = "LOCAL"
)

// RoutingNoType names the kind of routing identifier a bank account carries.
type RoutingNoType string

const (
	RoutingABA           RoutingNoType = "ABA"
	RoutingACH           RoutingNoType = "ACH"
	RoutingBIC           RoutingNoType = "BIC"
	RoutingBSB           RoutingNoType = "BSB"
	RoutingFedwire       RoutingNoType = "Fedwire"
	RoutingIBAN          RoutingNoType = "IBAN"
	RoutingIFSC          RoutingNoType = "IFSC"
	RoutingBranchCode    RoutingNoType = "Branch Code"
	RoutingCode          RoutingNoType = "Routing Code"
	RoutingSortCode      RoutingNoType = "Sort Code"
	RoutingSWIFT         RoutingNoType = "SWIFT"
	RoutingSWIFTBIC      RoutingNoType = "SWIFT/BIC"
	RoutingNumber        RoutingNoType = "Routing Number"
	RoutingCNAPS         RoutingNoType = "CNAPS"
	RoutingBankCode      RoutingNoType = "BANK CODE"
	RoutingNone          RoutingNoType = "None"
	RoutingTransitNumber RoutingNoType = "Transit Number"
)

// AccountType is a bank account's kind, where the destination country
// distinguishes them.
type AccountType string

const (
	AccountTypeSavings  AccountType = "SAVINGS"
	AccountTypeChecking AccountType = "CHECKING"
)

// BankAccount identifies a beneficiary's account at its bank.
//
// routingNoType1/routingNumber1 carry a second routing identifier where a
// destination needs two: a 12-digit CNAPS code for OURS payments into China,
// or a 6-digit Sort Code alongside an IBAN for the UK, Guernsey, Jersey and
// the Isle of Man.
type BankAccount struct {
	// CountryCode is the bank's ISO 3166-1 alpha-2 country.
	CountryCode string `json:"countryCode"`

	// BankType is SWIFT or LOCAL.
	BankType BankType `json:"bankType"`

	// BankName is the bank's name.
	BankName string `json:"bankName"`

	// AccountName is the name on the account. For AUD local beneficiaries it
	// is what Confirmation of Payee matches against.
	AccountName string `json:"accountName"`

	// RoutingNoType and RoutingNumber are the primary routing identifier.
	RoutingNoType RoutingNoType `json:"routingNoType,omitempty"`
	RoutingNumber string        `json:"routingNumber,omitempty"`

	// RoutingNoType1 and RoutingNumber1 are the conditional second identifier.
	RoutingNoType1 RoutingNoType `json:"routingNoType1,omitempty"`
	RoutingNumber1 string        `json:"routingNumber1,omitempty"`

	// AccountNumber is the account number, IBAN or equivalent.
	AccountNumber string `json:"accountNumber"`

	// AccountType is SAVINGS or CHECKING where the destination distinguishes.
	AccountType AccountType `json:"accountType,omitempty"`

	// Address is the bank's address.
	Address *Address `json:"address,omitempty"`

	// CurrencyCode is the account's currency. When a beneficiary is created
	// inline with a payment this is inherited from the payment and may be
	// left empty.
	CurrencyCode string `json:"currencyCode,omitempty"`
}

// IdentifierType names a personal identification document.
type IdentifierType string

const (
	IdentifierDriversLicense        IdentifierType = "Drivers License"
	IdentifierSocialSecurityNumber  IdentifierType = "Social Security Number"
	IdentifierPassport              IdentifierType = "Passport"
	IdentifierSocialInsuranceNumber IdentifierType = "Social Insurance Number"
	IdentifierNationalID            IdentifierType = "National Id"
)

// Relationship describes how a personal beneficiary relates to the payer.
type Relationship string

const (
	RelationshipSelf      Relationship = "Self"
	RelationshipFather    Relationship = "Father"
	RelationshipMother    Relationship = "Mother"
	RelationshipSpouse    Relationship = "Spouse"
	RelationshipSon       Relationship = "Son"
	RelationshipDaughter  Relationship = "Daughter"
	RelationshipBrother   Relationship = "Brother"
	RelationshipSister    Relationship = "Sister"
	RelationshipFriend    Relationship = "Friend"
	RelationshipEmployer  Relationship = "Employer"
	RelationshipColleague Relationship = "Colleague"
)

package capay

import "context"

// CustomersService manages the underlying customers you act for.
//
// The API calls these "clients" and serves them at /clients/business and
// /clients/personal; the guides call them Customers to distinguish them from
// you, the Client. Each customer is assigned its own clientId — there is no
// separate customerId — and passing that clientId on any other call makes the
// call act for that customer: its beneficiaries, its Sub-Account, its
// deposits.
//
// A newly created customer starts PENDING APPROVAL and cannot transact until
// Corporate Alliance activates it.
type CustomersService interface {
	// CreateBusiness onboards a business customer.
	CreateBusiness(ctx context.Context, req CreateBusinessCustomerRequest, opts ...RequestOption) (*BusinessCustomer, error)

	// ListBusinesses lists business customers.
	ListBusinesses(ctx context.Context, filter CustomerFilter) (*Page[BusinessCustomer], error)

	// GetBusiness fetches one business customer by id or by your referenceId.
	GetBusiness(ctx context.Context, idOrReferenceID string) (*BusinessCustomer, error)

	// UpdateBusiness amends a business customer. Only the fields in
	// UpdateBusinessCustomerRequest can change after creation.
	UpdateBusiness(ctx context.Context, id string, req UpdateBusinessCustomerRequest, opts ...RequestOption) (*BusinessCustomer, error)

	// DeleteBusiness deletes a business customer.
	DeleteBusiness(ctx context.Context, id string, opts ...RequestOption) error

	// CreatePersonal onboards a personal customer.
	CreatePersonal(ctx context.Context, req CreatePersonalCustomerRequest, opts ...RequestOption) (*PersonalCustomer, error)

	// ListPersonals lists personal customers.
	ListPersonals(ctx context.Context, filter CustomerFilter) (*Page[PersonalCustomer], error)

	// GetPersonal fetches one personal customer by id or by your referenceId.
	GetPersonal(ctx context.Context, idOrReferenceID string) (*PersonalCustomer, error)

	// UpdatePersonal amends a personal customer.
	UpdatePersonal(ctx context.Context, id string, req UpdatePersonalCustomerRequest, opts ...RequestOption) (*PersonalCustomer, error)

	// DeletePersonal deletes a personal customer.
	DeletePersonal(ctx context.Context, id string, opts ...RequestOption) error
}

// customersService implements CustomersService.
type customersService struct{ c *client }

// CustomerStatus is a customer's lifecycle state.
type CustomerStatus string

const (
	// CustomerPendingApproval — created, awaiting activation by Corporate
	// Alliance.
	CustomerPendingApproval CustomerStatus = "PENDING APPROVAL"

	// CustomerActive — ready to transact.
	CustomerActive CustomerStatus = "ACTIVE"

	// CustomerInactive — not currently usable.
	CustomerInactive CustomerStatus = "INACTIVE"

	// CustomerSuspended — blocked from transacting.
	CustomerSuspended CustomerStatus = "SUSPENDED"
)

// TransactionContinuation states whether a customer's expected activity is
// one-off or ongoing, for the KYC transaction profile.
type TransactionContinuation string

const (
	ContinuationOneOff  TransactionContinuation = "ONEOFF"
	ContinuationOngoing TransactionContinuation = "ONGOING"
)

// customerRecord holds the read-only fields the API adds to every customer.
type customerRecord struct {
	// ID is the customer's clientId — the value to pass on other calls to act
	// for this customer.
	ID string `json:"id"`

	// ClientNo is the human-readable customer number, e.g. "CAPAYAU-SYD-021233".
	ClientNo string `json:"clientNo"`

	// UniquePayID is the customer's CAPAY FastID, usable as a beneficiary's
	// UniquePayID for fee-free internal transfers.
	UniquePayID string `json:"uniquePayId"`

	// IsVerified reports approval. Deprecated by the API in favour of Status.
	IsVerified bool `json:"isVerified"`

	// Status is the lifecycle state.
	Status CustomerStatus `json:"status"`

	// CreatedTime and UpdatedTime are the record's timestamps.
	CreatedTime Time `json:"createdTime"`
	UpdatedTime Time `json:"updatedTime"`
}

// CreateBusinessCustomerRequest onboards a business customer.
type CreateBusinessCustomerRequest struct {
	// CountryCode is the ISO 3166-1 alpha-2 country of incorporation.
	CountryCode string `json:"countryCode"`

	// Address is the official business address. Required.
	Address Address `json:"address"`

	// Name is the registered name; TradingName the trading name; BusinessType
	// e.g. "Australian Proprietary Company". All required.
	Name         string `json:"name"`
	TradingName  string `json:"tradingName"`
	BusinessType string `json:"businessType"`

	// BusinessNumber is the government business number — the 11-digit ABN
	// for Australian businesses. Required. CompanyNumber is the 9-digit ACN.
	BusinessNumber string `json:"businessNumber"`
	CompanyNumber  string `json:"companyNumber,omitempty"`

	// BeneficiaryOwnerFirstName and BeneficiaryOwnerLastName name the
	// beneficial owner. Required.
	BeneficiaryOwnerFirstName string `json:"beneficiaryOwnerFirstName"`
	BeneficiaryOwnerLastName  string `json:"beneficiaryOwnerLastName"`

	// Optional descriptive fields.
	BusinessNature string `json:"businessNature,omitempty"`
	BusinessPhone  string `json:"businessPhone,omitempty"`
	ContactEmail   string `json:"contactEmail,omitempty"`
	ContactNumber  string `json:"contactNumber,omitempty"`
	RegisterDate   Date   `json:"registerDate,omitzero"`
	Website        string `json:"website,omitempty"`
	Note           string `json:"note,omitempty"`

	// Expected transaction profile, for KYC.
	TransactionContinuation         TransactionContinuation `json:"transactionContinuation,omitempty"`
	ExpectedAnnualTransactionAmount float64                 `json:"expectedAnnualTransactionAmount,omitempty"`
	ExpectedAnnualTransactionCount  int                     `json:"expectedAnnualTransactionCount,omitempty"`

	// ReferenceID is your own unique reference for this creation (≤ 36
	// characters). A repeated ReferenceID fails the creation, and the record
	// can later be fetched by it.
	ReferenceID string `json:"referenceId,omitempty"`
}

// BusinessCustomer is a business customer as the API returns it.
type BusinessCustomer struct {
	CreateBusinessCustomerRequest
	customerRecord
}

// UpdateBusinessCustomerRequest carries the fields a business customer allows
// to change after creation. Zero-valued fields are omitted, not cleared.
type UpdateBusinessCustomerRequest struct {
	CountryCode                     string                  `json:"countryCode,omitempty"`
	Address                         *Address                `json:"address,omitempty"`
	ContactEmail                    string                  `json:"contactEmail,omitempty"`
	Note                            string                  `json:"note,omitempty"`
	TransactionContinuation         TransactionContinuation `json:"transactionContinuation,omitempty"`
	ExpectedAnnualTransactionAmount float64                 `json:"expectedAnnualTransactionAmount,omitempty"`
	ExpectedAnnualTransactionCount  int                     `json:"expectedAnnualTransactionCount,omitempty"`
	TradingName                     string                  `json:"tradingName,omitempty"`
	BusinessNature                  string                  `json:"businessNature,omitempty"`
	BusinessPhone                   string                  `json:"businessPhone,omitempty"`
	BeneficiaryOwnerFirstName       string                  `json:"beneficiaryOwnerFirstName,omitempty"`
	BeneficiaryOwnerLastName        string                  `json:"beneficiaryOwnerLastName,omitempty"`
	Website                         string                  `json:"website,omitempty"`
}

// CreatePersonalCustomerRequest onboards a personal customer.
type CreatePersonalCustomerRequest struct {
	// CountryCode is the ISO 3166-1 alpha-2 country of residence.
	CountryCode string `json:"countryCode"`

	// Address is the residential address. Required.
	Address Address `json:"address"`

	// FirstName, LastName, Nationality (ISO 3166-1 alpha-2) and DateOfBirth
	// are required.
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Nationality string `json:"nationality"`
	DateOfBirth Date   `json:"dateOfBirth"`

	// Optional descriptive fields.
	Phone        string `json:"phone,omitempty"`
	ContactEmail string `json:"contactEmail,omitempty"`
	Occupation   string `json:"occupation,omitempty"`
	Note         string `json:"note,omitempty"`

	// Identification document. Type, Number and IssueCountry must all be
	// provided together to be saved.
	IdentificationType         IdentifierType `json:"identificationType,omitempty"`
	IdentificationNumber       string         `json:"identificationNumber,omitempty"`
	IdentificationExpiryDate   Date           `json:"identificationExpiryDate,omitzero"`
	IdentificationIssueCountry string         `json:"identificationIssueCountry,omitempty"`

	// Expected transaction profile, for KYC.
	TransactionContinuation         TransactionContinuation `json:"transactionContinuation,omitempty"`
	ExpectedAnnualTransactionAmount float64                 `json:"expectedAnnualTransactionAmount,omitempty"`
	ExpectedAnnualTransactionCount  int                     `json:"expectedAnnualTransactionCount,omitempty"`

	// ReferenceID is your own unique reference for this creation.
	ReferenceID string `json:"referenceId,omitempty"`
}

// PersonalCustomer is a personal customer as the API returns it.
type PersonalCustomer struct {
	CreatePersonalCustomerRequest
	customerRecord
}

// UpdatePersonalCustomerRequest carries the fields a personal customer allows
// to change after creation. Zero-valued fields are omitted, not cleared.
type UpdatePersonalCustomerRequest struct {
	CountryCode                     string                  `json:"countryCode,omitempty"`
	Address                         *Address                `json:"address,omitempty"`
	ContactEmail                    string                  `json:"contactEmail,omitempty"`
	Note                            string                  `json:"note,omitempty"`
	TransactionContinuation         TransactionContinuation `json:"transactionContinuation,omitempty"`
	ExpectedAnnualTransactionAmount float64                 `json:"expectedAnnualTransactionAmount,omitempty"`
	ExpectedAnnualTransactionCount  int                     `json:"expectedAnnualTransactionCount,omitempty"`
	Phone                           string                  `json:"phone,omitempty"`
	Nationality                     string                  `json:"nationality,omitempty"`
	Occupation                      string                  `json:"occupation,omitempty"`
	IdentificationType              IdentifierType          `json:"identificationType,omitempty"`
	IdentificationNumber            string                  `json:"identificationNumber,omitempty"`
	IdentificationExpiryDate        Date                    `json:"identificationExpiryDate,omitzero"`
	IdentificationIssueCountry      string                  `json:"identificationIssueCountry,omitempty"`
}

// CustomerFilter narrows a list call.
type CustomerFilter struct {
	ListOptions

	// CountryCode restricts by country.
	CountryCode string

	// Name is a partial or full name match.
	Name string
}

func (f CustomerFilter) query() *queryBuilder {
	return newQuery().
		page(f.ListOptions).
		str("countryCode", f.CountryCode).
		str("name", f.Name)
}

// ---- Business ---------------------------------------------------------------

// CreateBusiness implements CustomersService.
func (s *customersService) CreateBusiness(ctx context.Context, req CreateBusinessCustomerRequest, opts ...RequestOption) (*BusinessCustomer, error) {
	switch {
	case req.CountryCode == "":
		return nil, invalidInput("CountryCode is required")
	case req.Name == "" || req.TradingName == "" || req.BusinessType == "":
		return nil, invalidInput("Name, TradingName and BusinessType are required")
	case req.BusinessNumber == "":
		return nil, invalidInput("BusinessNumber is required")
	case req.BeneficiaryOwnerFirstName == "" || req.BeneficiaryOwnerLastName == "":
		return nil, invalidInput("BeneficiaryOwnerFirstName and BeneficiaryOwnerLastName are required")
	}
	var out BusinessCustomer
	if err := s.c.mutate(ctx, apiBusinessCustomerCreate, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListBusinesses implements CustomersService.
func (s *customersService) ListBusinesses(ctx context.Context, filter CustomerFilter) (*Page[BusinessCustomer], error) {
	var out Page[BusinessCustomer]
	if err := s.c.do(ctx, requestSpec{api: apiBusinessCustomerList, query: filter.query().values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBusiness implements CustomersService.
func (s *customersService) GetBusiness(ctx context.Context, idOrReferenceID string) (*BusinessCustomer, error) {
	if idOrReferenceID == "" {
		return nil, invalidInput("customer id is required")
	}
	var out BusinessCustomer
	if err := s.c.do(ctx, requestSpec{api: apiBusinessCustomerGet, pathParams: idParam(idOrReferenceID), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateBusiness implements CustomersService.
func (s *customersService) UpdateBusiness(ctx context.Context, id string, req UpdateBusinessCustomerRequest, opts ...RequestOption) (*BusinessCustomer, error) {
	if id == "" {
		return nil, invalidInput("customer id is required")
	}
	var out BusinessCustomer
	if err := s.c.mutate(ctx, apiBusinessCustomerUpdate, idParam(id), req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBusiness implements CustomersService.
func (s *customersService) DeleteBusiness(ctx context.Context, id string, opts ...RequestOption) error {
	if id == "" {
		return invalidInput("customer id is required")
	}
	return s.c.mutate(ctx, apiBusinessCustomerDelete, idParam(id), nil, nil, opts)
}

// ---- Personal ---------------------------------------------------------------

// CreatePersonal implements CustomersService.
func (s *customersService) CreatePersonal(ctx context.Context, req CreatePersonalCustomerRequest, opts ...RequestOption) (*PersonalCustomer, error) {
	switch {
	case req.CountryCode == "":
		return nil, invalidInput("CountryCode is required")
	case req.FirstName == "" || req.LastName == "":
		return nil, invalidInput("FirstName and LastName are required")
	case req.Nationality == "":
		return nil, invalidInput("Nationality is required")
	case req.DateOfBirth.IsZero():
		return nil, invalidInput("DateOfBirth is required")
	}
	var out PersonalCustomer
	if err := s.c.mutate(ctx, apiPersonalCustomerCreate, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPersonals implements CustomersService.
func (s *customersService) ListPersonals(ctx context.Context, filter CustomerFilter) (*Page[PersonalCustomer], error) {
	var out Page[PersonalCustomer]
	if err := s.c.do(ctx, requestSpec{api: apiPersonalCustomerList, query: filter.query().values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPersonal implements CustomersService.
func (s *customersService) GetPersonal(ctx context.Context, idOrReferenceID string) (*PersonalCustomer, error) {
	if idOrReferenceID == "" {
		return nil, invalidInput("customer id is required")
	}
	var out PersonalCustomer
	if err := s.c.do(ctx, requestSpec{api: apiPersonalCustomerGet, pathParams: idParam(idOrReferenceID), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdatePersonal implements CustomersService.
func (s *customersService) UpdatePersonal(ctx context.Context, id string, req UpdatePersonalCustomerRequest, opts ...RequestOption) (*PersonalCustomer, error) {
	if id == "" {
		return nil, invalidInput("customer id is required")
	}
	var out PersonalCustomer
	if err := s.c.mutate(ctx, apiPersonalCustomerUpdate, idParam(id), req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePersonal implements CustomersService.
func (s *customersService) DeletePersonal(ctx context.Context, id string, opts ...RequestOption) error {
	if id == "" {
		return invalidInput("customer id is required")
	}
	return s.c.mutate(ctx, apiPersonalCustomerDelete, idParam(id), nil, nil, opts)
}

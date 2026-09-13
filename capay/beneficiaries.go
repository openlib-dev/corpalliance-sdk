package capay

import "context"

// BeneficiariesService exposes payees. Beneficiaries come in two types —
// business and personal — each with its own endpoints. Once created, a
// beneficiary can be reused across many payments.
//
// Ownership is decided at creation by ClientID: omit it and the beneficiary is
// yours; supply a customer's clientId and it belongs to that customer. It
// cannot be changed afterwards, and a beneficiary with an in-progress payment
// cannot be updated or deleted.
//
// Duplicate detection is per owner: clientId + uniquePayId, or clientId + bank
// details (routing number + account number + account name + currency code). A
// duplicate is rejected with HTTP 400.
type BeneficiariesService interface {
	// CreateBusiness creates a business payee.
	CreateBusiness(ctx context.Context, req BusinessBeneficiaryRequest, opts ...RequestOption) (*BusinessBeneficiary, error)

	// ListBusinesses lists business payees.
	ListBusinesses(ctx context.Context, filter BeneficiaryFilter) (*Page[BusinessBeneficiary], error)

	// GetBusiness fetches one business payee by id or by your referenceId.
	GetBusiness(ctx context.Context, idOrReferenceID string) (*BusinessBeneficiary, error)

	// UpdateBusiness replaces a business payee's details. It is a PUT: send
	// the full record, not a patch. ClientID and ReferenceID are not part of
	// the update payload and are ignored if set.
	UpdateBusiness(ctx context.Context, id string, req BusinessBeneficiaryRequest, opts ...RequestOption) (*BusinessBeneficiary, error)

	// DeleteBusiness deletes a business payee, returning its final state.
	DeleteBusiness(ctx context.Context, id string, opts ...RequestOption) (*BusinessBeneficiary, error)

	// ValidateBusiness checks creation data for blocking errors without
	// creating anything. A nil error means the data would be accepted;
	// otherwise the error is the 400 the create call would have returned.
	// No Confirmation of Payee check is performed.
	ValidateBusiness(ctx context.Context, req BusinessBeneficiaryRequest) error

	// ConfirmBusinessPayee runs the same checks as ValidateBusiness and, for
	// local AUD accounts, a Confirmation of Payee name match. Strongly
	// recommended before creating a local AUD beneficiary.
	ConfirmBusinessPayee(ctx context.Context, req BusinessBeneficiaryRequest) (*PayeeConfirmation, error)

	// CreatePersonal creates a personal payee.
	CreatePersonal(ctx context.Context, req PersonalBeneficiaryRequest, opts ...RequestOption) (*PersonalBeneficiary, error)

	// ListPersonals lists personal payees.
	ListPersonals(ctx context.Context, filter BeneficiaryFilter) (*Page[PersonalBeneficiary], error)

	// GetPersonal fetches one personal payee by id or by your referenceId.
	GetPersonal(ctx context.Context, idOrReferenceID string) (*PersonalBeneficiary, error)

	// UpdatePersonal replaces a personal payee's details; see UpdateBusiness.
	UpdatePersonal(ctx context.Context, id string, req PersonalBeneficiaryRequest, opts ...RequestOption) (*PersonalBeneficiary, error)

	// DeletePersonal deletes a personal payee, returning its final state.
	DeletePersonal(ctx context.Context, id string, opts ...RequestOption) (*PersonalBeneficiary, error)

	// ValidatePersonal checks creation data; see ValidateBusiness.
	ValidatePersonal(ctx context.Context, req PersonalBeneficiaryRequest) error

	// ConfirmPersonalPayee runs Confirmation of Payee; see ConfirmBusinessPayee.
	ConfirmPersonalPayee(ctx context.Context, req PersonalBeneficiaryRequest) (*PayeeConfirmation, error)
}

// beneficiariesService implements BeneficiariesService.
type beneficiariesService struct{ c *client }

// beneficiaryIdentity is what both request types expose for shared local
// validation.
type beneficiaryIdentity interface {
	identity() (countryCode, uniquePayID string, bankAccount *BankAccount)
}

// validateBeneficiary applies the "either uniquePayId or bankAccount" rule
// locally.
func validateBeneficiary(b beneficiaryIdentity) error {
	countryCode, uniquePayID, bankAccount := b.identity()
	if countryCode == "" {
		return invalidInput("CountryCode is required")
	}
	if uniquePayID == "" && bankAccount == nil {
		return invalidInput("either UniquePayID or BankAccount is required")
	}
	return nil
}

// BusinessBeneficiaryRequest creates or updates a business payee.
type BusinessBeneficiaryRequest struct {
	// CountryCode is the beneficiary's ISO 3166-1 alpha-2 country.
	CountryCode string `json:"countryCode"`

	// Email and PhoneNumber (E.164) are optional contact details.
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`

	// Address is the beneficiary's address.
	Address *Address `json:"address,omitempty"`

	// UniquePayID is the beneficiary's CAPAY FastID, if they are also a
	// Corporate Alliance client. Paying by FastID is a fee-free internal
	// transfer. Either UniquePayID or BankAccount is required.
	UniquePayID string `json:"uniquePayId,omitempty"`

	// BankAccount is the beneficiary's bank account. Either UniquePayID or
	// BankAccount is required.
	BankAccount *BankAccount `json:"bankAccount,omitempty"`

	// ClientID makes the beneficiary belong to one of your customers. Empty
	// means it belongs to you. Accepted at creation only.
	ClientID string `json:"clientId,omitempty"`

	// ReferenceID is your own unique reference for this creation (≤ 36
	// characters of letters, digits, underscore and hyphen). Optional but
	// recommended: a repeated ReferenceID fails the creation, and the record
	// can later be fetched by it. Accepted at creation only.
	ReferenceID string `json:"referenceId,omitempty"`

	// Name is the business's name (≤ 128 characters of letters, digits,
	// spaces and / & - . , ' +).
	Name string `json:"name"`

	// NickName is an optional short label.
	NickName string `json:"nickName,omitempty"`
}

func (r BusinessBeneficiaryRequest) identity() (string, string, *BankAccount) {
	return r.CountryCode, r.UniquePayID, r.BankAccount
}

// updateBody strips the creation-only fields for a PUT.
func (r BusinessBeneficiaryRequest) updateBody() BusinessBeneficiaryRequest {
	r.ClientID = ""
	r.ReferenceID = ""
	return r
}

// BusinessBeneficiary is a business payee as the API returns it.
type BusinessBeneficiary struct {
	BusinessBeneficiaryRequest

	// ID is the beneficiary's id — the value to pass as Payment.BeneficiaryID.
	ID string `json:"id"`

	// IsActive reports whether the beneficiary can be paid.
	IsActive bool `json:"isActive"`

	// CreatedTime is when the beneficiary was created.
	CreatedTime Time `json:"createdTime"`
}

// PersonalBeneficiaryRequest creates or updates a personal payee.
type PersonalBeneficiaryRequest struct {
	// CountryCode is the beneficiary's ISO 3166-1 alpha-2 country.
	CountryCode string `json:"countryCode"`

	// Email and PhoneNumber (E.164) are optional contact details.
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`

	// Address is the beneficiary's address.
	Address *Address `json:"address,omitempty"`

	// UniquePayID is the beneficiary's CAPAY FastID, if they are also a
	// Corporate Alliance client. Paying by FastID is a fee-free internal
	// transfer. Either UniquePayID or BankAccount is required.
	UniquePayID string `json:"uniquePayId,omitempty"`

	// BankAccount is the beneficiary's bank account. Either UniquePayID or
	// BankAccount is required.
	BankAccount *BankAccount `json:"bankAccount,omitempty"`

	// ClientID makes the beneficiary belong to one of your customers. Empty
	// means it belongs to you. Accepted at creation only.
	ClientID string `json:"clientId,omitempty"`

	// ReferenceID is your own unique reference for this creation (≤ 36
	// characters of letters, digits, underscore and hyphen). Optional but
	// recommended: a repeated ReferenceID fails the creation, and the record
	// can later be fetched by it. Accepted at creation only.
	ReferenceID string `json:"referenceId,omitempty"`

	// FirstName and LastName (each ≤ 32 characters of letters, spaces,
	// apostrophes and hyphens).
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`

	// IdentifierType and Identifier optionally record an identity document.
	IdentifierType IdentifierType `json:"identifierType,omitempty"`
	Identifier     string         `json:"identifier,omitempty"`

	// Relationship is the payee's relationship to the payer.
	Relationship Relationship `json:"relationship,omitempty"`
}

func (r PersonalBeneficiaryRequest) identity() (string, string, *BankAccount) {
	return r.CountryCode, r.UniquePayID, r.BankAccount
}

// updateBody strips the creation-only fields for a PUT.
func (r PersonalBeneficiaryRequest) updateBody() PersonalBeneficiaryRequest {
	r.ClientID = ""
	r.ReferenceID = ""
	return r
}

// PersonalBeneficiary is a personal payee as the API returns it.
type PersonalBeneficiary struct {
	PersonalBeneficiaryRequest

	// ID is the beneficiary's id — the value to pass as Payment.BeneficiaryID.
	ID string `json:"id"`

	// IsActive reports whether the beneficiary can be paid.
	IsActive bool `json:"isActive"`

	// CreatedTime is when the beneficiary was created.
	CreatedTime Time `json:"createdTime"`
}

// BeneficiaryFilter narrows a list call.
type BeneficiaryFilter struct {
	ListOptions

	// CountryCode and CurrencyCode restrict by the beneficiary's country and
	// bank account currency.
	CountryCode  string
	CurrencyCode string

	// ClientID scopes the list to a customer's beneficiaries. Empty means
	// your own.
	ClientID string

	// Name is a partial or full name match. Business beneficiaries only; the
	// personal list endpoint does not accept it.
	Name string
}

func (f BeneficiaryFilter) query() *queryBuilder {
	return newQuery().
		page(f.ListOptions).
		str("countryCode", f.CountryCode).
		str("currencyCode", f.CurrencyCode).
		str("clientId", f.ClientID).
		str("name", f.Name)
}

// CoPCode is the outcome of a Confirmation of Payee check.
type CoPCode string

const (
	// CoPMatched — the name matches the account.
	CoPMatched CoPCode = "COP MATCHED"

	// CoPCloseMatched — a near match; check PayeeConfirmation.AccountNames
	// for the suggested name and its match score.
	CoPCloseMatched CoPCode = "COP CLOSE MATCHED"

	// CoPNotMatched — the name does not match.
	CoPNotMatched CoPCode = "COP NOT MATCHED"

	// CoPAccountNotFound — the account could not be found.
	CoPAccountNotFound CoPCode = "COP ACC NOT FOUND"

	// CoPOptionalOut — the account holder has opted out of CoP.
	CoPOptionalOut CoPCode = "COP OPTIONAL OUT"

	// CoPOthers — CoP not applicable (a non-AUD or SWIFT beneficiary, an
	// unknown BSB) or some other outcome; read Message.
	CoPOthers CoPCode = "COP OTHERS"
)

// PayeeConfirmation is the result of a Confirmation of Payee check.
type PayeeConfirmation struct {
	// Code is the outcome.
	Code CoPCode `json:"code"`

	// Message is the API's explanation, e.g. "Account close matched" or
	// "Invalid responder BIC.".
	Message string `json:"message"`

	// CoPDetails carries the matched or suggested names when Code is MATCHED
	// or CLOSE MATCHED.
	CoPDetails *CoPDetails `json:"copDetails,omitempty"`
}

// CoPDetails holds the account names Confirmation of Payee returned.
type CoPDetails struct {
	AccountNames []CoPAccountName `json:"accountNames"`
}

// CoPAccountName is one candidate name with its match score.
type CoPAccountName struct {
	// AccountName is the name on record at the receiving bank.
	AccountName string `json:"accountName"`

	// MatchScore is 0–100.
	MatchScore float64 `json:"matchScore"`
}

// ---- Business ---------------------------------------------------------------

// CreateBusiness implements BeneficiariesService.
func (s *beneficiariesService) CreateBusiness(ctx context.Context, req BusinessBeneficiaryRequest, opts ...RequestOption) (*BusinessBeneficiary, error) {
	if err := validateBeneficiary(req); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, invalidInput("Name is required for a business beneficiary")
	}
	var out BusinessBeneficiary
	if err := s.c.mutate(ctx, apiBusinessBeneficiaryCreate, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListBusinesses implements BeneficiariesService.
func (s *beneficiariesService) ListBusinesses(ctx context.Context, filter BeneficiaryFilter) (*Page[BusinessBeneficiary], error) {
	var out Page[BusinessBeneficiary]
	if err := s.c.do(ctx, requestSpec{api: apiBusinessBeneficiaryList, query: filter.query().values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBusiness implements BeneficiariesService.
func (s *beneficiariesService) GetBusiness(ctx context.Context, idOrReferenceID string) (*BusinessBeneficiary, error) {
	if idOrReferenceID == "" {
		return nil, invalidInput("beneficiary id is required")
	}
	var out BusinessBeneficiary
	if err := s.c.do(ctx, requestSpec{api: apiBusinessBeneficiaryGet, pathParams: idParam(idOrReferenceID), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateBusiness implements BeneficiariesService.
func (s *beneficiariesService) UpdateBusiness(ctx context.Context, id string, req BusinessBeneficiaryRequest, opts ...RequestOption) (*BusinessBeneficiary, error) {
	if id == "" {
		return nil, invalidInput("beneficiary id is required")
	}
	if req.Name == "" {
		return nil, invalidInput("Name is required for a business beneficiary")
	}
	var out BusinessBeneficiary
	if err := s.c.mutate(ctx, apiBusinessBeneficiaryUpdate, idParam(id), req.updateBody(), &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBusiness implements BeneficiariesService.
func (s *beneficiariesService) DeleteBusiness(ctx context.Context, id string, opts ...RequestOption) (*BusinessBeneficiary, error) {
	if id == "" {
		return nil, invalidInput("beneficiary id is required")
	}
	var out BusinessBeneficiary
	if err := s.c.mutate(ctx, apiBusinessBeneficiaryDelete, idParam(id), nil, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ValidateBusiness implements BeneficiariesService.
func (s *beneficiariesService) ValidateBusiness(ctx context.Context, req BusinessBeneficiaryRequest) error {
	if err := validateBeneficiary(req); err != nil {
		return err
	}
	return s.c.do(ctx, requestSpec{api: apiBusinessBeneficiaryValidate, body: req})
}

// ConfirmBusinessPayee implements BeneficiariesService.
func (s *beneficiariesService) ConfirmBusinessPayee(ctx context.Context, req BusinessBeneficiaryRequest) (*PayeeConfirmation, error) {
	if err := validateBeneficiary(req); err != nil {
		return nil, err
	}
	var out PayeeConfirmation
	if err := s.c.do(ctx, requestSpec{api: apiBusinessBeneficiaryCoP, body: req, result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Personal ---------------------------------------------------------------

// CreatePersonal implements BeneficiariesService.
func (s *beneficiariesService) CreatePersonal(ctx context.Context, req PersonalBeneficiaryRequest, opts ...RequestOption) (*PersonalBeneficiary, error) {
	if err := validateBeneficiary(req); err != nil {
		return nil, err
	}
	if req.FirstName == "" || req.LastName == "" {
		return nil, invalidInput("FirstName and LastName are required for a personal beneficiary")
	}
	var out PersonalBeneficiary
	if err := s.c.mutate(ctx, apiPersonalBeneficiaryCreate, nil, req, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPersonals implements BeneficiariesService.
func (s *beneficiariesService) ListPersonals(ctx context.Context, filter BeneficiaryFilter) (*Page[PersonalBeneficiary], error) {
	var out Page[PersonalBeneficiary]
	if err := s.c.do(ctx, requestSpec{api: apiPersonalBeneficiaryList, query: filter.query().values(), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPersonal implements BeneficiariesService.
func (s *beneficiariesService) GetPersonal(ctx context.Context, idOrReferenceID string) (*PersonalBeneficiary, error) {
	if idOrReferenceID == "" {
		return nil, invalidInput("beneficiary id is required")
	}
	var out PersonalBeneficiary
	if err := s.c.do(ctx, requestSpec{api: apiPersonalBeneficiaryGet, pathParams: idParam(idOrReferenceID), result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdatePersonal implements BeneficiariesService.
func (s *beneficiariesService) UpdatePersonal(ctx context.Context, id string, req PersonalBeneficiaryRequest, opts ...RequestOption) (*PersonalBeneficiary, error) {
	if id == "" {
		return nil, invalidInput("beneficiary id is required")
	}
	if req.FirstName == "" || req.LastName == "" {
		return nil, invalidInput("FirstName and LastName are required for a personal beneficiary")
	}
	var out PersonalBeneficiary
	if err := s.c.mutate(ctx, apiPersonalBeneficiaryUpdate, idParam(id), req.updateBody(), &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePersonal implements BeneficiariesService.
func (s *beneficiariesService) DeletePersonal(ctx context.Context, id string, opts ...RequestOption) (*PersonalBeneficiary, error) {
	if id == "" {
		return nil, invalidInput("beneficiary id is required")
	}
	var out PersonalBeneficiary
	if err := s.c.mutate(ctx, apiPersonalBeneficiaryDelete, idParam(id), nil, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// ValidatePersonal implements BeneficiariesService.
func (s *beneficiariesService) ValidatePersonal(ctx context.Context, req PersonalBeneficiaryRequest) error {
	if err := validateBeneficiary(req); err != nil {
		return err
	}
	return s.c.do(ctx, requestSpec{api: apiPersonalBeneficiaryValidate, body: req})
}

// ConfirmPersonalPayee implements BeneficiariesService.
func (s *beneficiariesService) ConfirmPersonalPayee(ctx context.Context, req PersonalBeneficiaryRequest) (*PayeeConfirmation, error) {
	if err := validateBeneficiary(req); err != nil {
		return nil, err
	}
	var out PayeeConfirmation
	if err := s.c.do(ctx, requestSpec{api: apiPersonalBeneficiaryCoP, body: req, result: &out}); err != nil {
		return nil, err
	}
	return &out, nil
}

// idParam builds the path parameter map for a {id} endpoint.
func idParam(id string) map[string]string { return map[string]string{"id": id} }

// mutate is the shared shape of every create/update/delete call: apply the
// per-call options (idempotency key), send, decode.
func (c *client) mutate(ctx context.Context, a api, pathParams map[string]string, body, result any, opts []RequestOption) error {
	spec := requestSpec{api: a, pathParams: pathParams, body: body, result: result}
	if err := spec.apply(opts); err != nil {
		return err
	}
	return c.do(ctx, spec)
}

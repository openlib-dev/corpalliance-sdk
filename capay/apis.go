package capay

import "net/http"

// Environment selects which CAPAY deployment the client talks to.
type Environment string

const (
	// Sandbox is api.sandbox.capay.com.au. No real money moves there, and
	// Deposits().Simulate is available to drive the full flow end to end.
	Sandbox Environment = "sandbox"

	// Production is api.capay.com.au. Credentials are issued separately per
	// environment and are not interchangeable.
	Production Environment = "production"
)

// Base URLs per environment, as documented on the Authentication page.
const (
	sandboxBaseURL    = "https://api.sandbox.capay.com.au"
	productionBaseURL = "https://api.capay.com.au"
)

// baseURLFor returns the default base URL for an environment.
func baseURLFor(env Environment) string {
	if env == Production {
		return productionBaseURL
	}
	return sandboxBaseURL
}

// api describes a single endpoint: verb plus path relative to the base URL.
// Paths carry {placeholders} that client.resolvePath substitutes.
type api struct {
	Method string
	Path   string
}

// The CAPAY Client API endpoint catalogue.
//
// Only the Confirmation-of-Payee validators are versioned (/v2); everything
// else is unversioned. That is the API's layout, reproduced from its OpenAPI
// schema.
var (
	// ---- Authentication -------------------------------------------------

	apiAuthenticate = api{http.MethodPost, "/authenticate"}

	// ---- Accounts ---------------------------------------------------------

	apiBalances               = api{http.MethodGet, "/accounts/balances"}
	apiVirtualAccountList     = api{http.MethodGet, "/accounts/virtual"}
	apiVirtualAccountCreate   = api{http.MethodPost, "/accounts/virtual"}
	apiVirtualAccountPayID    = api{http.MethodPatch, "/accounts/virtual/{id}/payid"}
	apiVirtualAccountDisable  = api{http.MethodPatch, "/accounts/virtual/{id}/disable"}
	apiVirtualAccountActivate = api{http.MethodPatch, "/accounts/virtual/{id}/activate"}

	// ---- Transactions -----------------------------------------------------

	apiTransactions = api{http.MethodGet, "/transactions"}

	// ---- Beneficiaries ----------------------------------------------------
	//
	// Note the singular/plural mismatch the API ships with: the business
	// collection is listed at /beneficiaries/businesses but addressed at
	// /beneficiaries/business/{id}, while personal uses /personals for both.

	apiBusinessBeneficiaryCreate   = api{http.MethodPost, "/beneficiaries/business"}
	apiBusinessBeneficiaryList     = api{http.MethodGet, "/beneficiaries/businesses"}
	apiBusinessBeneficiaryGet      = api{http.MethodGet, "/beneficiaries/business/{id}"}
	apiBusinessBeneficiaryUpdate   = api{http.MethodPut, "/beneficiaries/business/{id}"}
	apiBusinessBeneficiaryDelete   = api{http.MethodDelete, "/beneficiaries/business/{id}"}
	apiBusinessBeneficiaryValidate = api{http.MethodPost, "/beneficiaries/business/validate"}
	apiBusinessBeneficiaryCoP      = api{http.MethodPost, "/v2/beneficiaries/business/validate"}

	apiPersonalBeneficiaryCreate   = api{http.MethodPost, "/beneficiaries/personals"}
	apiPersonalBeneficiaryList     = api{http.MethodGet, "/beneficiaries/personals"}
	apiPersonalBeneficiaryGet      = api{http.MethodGet, "/beneficiaries/personals/{id}"}
	apiPersonalBeneficiaryUpdate   = api{http.MethodPut, "/beneficiaries/personals/{id}"}
	apiPersonalBeneficiaryDelete   = api{http.MethodDelete, "/beneficiaries/personals/{id}"}
	apiPersonalBeneficiaryValidate = api{http.MethodPost, "/beneficiaries/personals/validate"}
	apiPersonalBeneficiaryCoP      = api{http.MethodPost, "/v2/beneficiaries/personals/validate"}

	// ---- Customers (the API's /clients) -----------------------------------
	//
	// The same singular/plural quirk: businesses are listed at /businesses,
	// personals at /personal.

	apiBusinessCustomerCreate = api{http.MethodPost, "/clients/business"}
	apiBusinessCustomerList   = api{http.MethodGet, "/clients/businesses"}
	apiBusinessCustomerGet    = api{http.MethodGet, "/clients/business/{id}"}
	apiBusinessCustomerUpdate = api{http.MethodPut, "/clients/business/{id}"}
	apiBusinessCustomerDelete = api{http.MethodDelete, "/clients/business/{id}"}

	apiPersonalCustomerCreate = api{http.MethodPost, "/clients/personal"}
	apiPersonalCustomerList   = api{http.MethodGet, "/clients/personal"}
	apiPersonalCustomerGet    = api{http.MethodGet, "/clients/personal/{id}"}
	apiPersonalCustomerUpdate = api{http.MethodPut, "/clients/personal/{id}"}
	apiPersonalCustomerDelete = api{http.MethodDelete, "/clients/personal/{id}"}

	// ---- Payments ---------------------------------------------------------

	apiPaymentCreate          = api{http.MethodPost, "/payments"}
	apiPaymentCreateWithBenef = api{http.MethodPost, "/payments/with-beneficiary"}
	apiPaymentQuotation       = api{http.MethodPost, "/payments/quotation"}
	apiPaymentList            = api{http.MethodGet, "/payments"}
	apiPaymentGet             = api{http.MethodGet, "/payments/{id}"}
	apiPaymentUpdate          = api{http.MethodPut, "/payments/{id}"}
	apiPaymentCancel          = api{http.MethodPut, "/payments/cancel/{id}"}

	// ---- Deposits ---------------------------------------------------------

	apiDepositList     = api{http.MethodGet, "/deposits"}
	apiDepositSimulate = api{http.MethodPost, "/deposits/simulate"}

	// ---- Rates ------------------------------------------------------------

	apiRateDaily = api{http.MethodGet, "/rates/daily"}

	// ---- Webhooks ---------------------------------------------------------

	apiWebhookCreate = api{http.MethodPost, "/webhooks"}
	apiWebhookList   = api{http.MethodGet, "/webhooks"}
	apiWebhookUpdate = api{http.MethodPut, "/webhooks/{id}"}
	apiWebhookDelete = api{http.MethodDelete, "/webhooks/{id}"}
)

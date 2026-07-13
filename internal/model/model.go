package model

import "encoding/json"

type User struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}
type AuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type Transaction struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	OccurredAt  string `json:"occurred_at"`
}
type TransactionRequest struct {
	Type        string `json:"type"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	OccurredAt  string `json:"occurred_at"`
}
type Category struct {
	ID        int    `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}
type CategoryRequest struct {
	Type string `json:"type"`
	Name string `json:"name"`
}
type Summary struct {
	Month            string `json:"month"`
	Income           string `json:"income"`
	Expense          string `json:"expense"`
	Balance          string `json:"balance"`
	Currency         string `json:"currency"`
	TransactionCount int    `json:"transaction_count"`
}

type ImportResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
	Ignored  int `json:"ignored"`
}

type ImportedTransaction struct {
	Request     TransactionRequest
	Fingerprint string
}

type OpenBankingInstitution struct {
	Name                   string                  `json:"name"`
	Country                string                  `json:"country"`
	Logo                   string                  `json:"logo"`
	PSUTypes               []string                `json:"psu_types"`
	AuthMethods            []OpenBankingAuthMethod `json:"auth_methods"`
	MaximumConsentValidity int64                   `json:"maximum_consent_validity"`
	Beta                   bool                    `json:"beta"`
	BIC                    string                  `json:"bic,omitempty"`
	RequiredPSUHeaders     []string                `json:"required_psu_headers,omitempty"`
}

type OpenBankingAuthMethod struct {
	Name         string `json:"name"`
	Title        string `json:"title,omitempty"`
	PSUType      string `json:"psu_type"`
	Approach     string `json:"approach"`
	HiddenMethod bool   `json:"hidden_method"`
}

type OpenBankingAuthorizationRequest struct {
	InstitutionName string `json:"institution_name"`
	Country         string `json:"country"`
	PSUType         string `json:"psu_type,omitempty"`
	ConsentDays     int    `json:"consent_days,omitempty"`
	Language        string `json:"language,omitempty"`
}

type OpenBankingAuthorization struct {
	AuthorizationURL string `json:"authorization_url"`
	AuthorizationID  string `json:"authorization_id"`
	ValidUntil       string `json:"valid_until"`
	ExpiresAt        string `json:"expires_at"`
}

type OpenBankingCallbackRequest struct {
	State            string
	Code             string
	Error            string
	ErrorDescription string
}

type OpenBankingCallbackResult struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	ConnectionID int    `json:"connection_id,omitempty"`
	RedirectURL  string `json:"-"`
}

type OpenBankingConnection struct {
	ID              int    `json:"id"`
	InstitutionName string `json:"institution_name"`
	Country         string `json:"country"`
	PSUType         string `json:"psu_type"`
	Status          string `json:"status"`
	ValidUntil      string `json:"valid_until"`
	AccountCount    int    `json:"account_count"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type OpenBankingAccount struct {
	ID                 int    `json:"id"`
	ConnectionID       int    `json:"connection_id"`
	InstitutionName    string `json:"institution_name"`
	Country            string `json:"country"`
	Name               string `json:"name,omitempty"`
	Details            string `json:"details,omitempty"`
	CashAccountType    string `json:"cash_account_type"`
	Product            string `json:"product,omitempty"`
	Currency           string `json:"currency"`
	DisplayIdentifier  string `json:"display_identifier,omitempty"`
	IdentificationHash string `json:"identification_hash"`
	CanFetchData       bool   `json:"can_fetch_data"`
}

type OpenBankingPSUContext struct {
	IPAddress      string
	UserAgent      string
	Referer        string
	Accept         string
	AcceptCharset  string
	AcceptEncoding string
	AcceptLanguage string
}

type OpenBankingProviderData = json.RawMessage

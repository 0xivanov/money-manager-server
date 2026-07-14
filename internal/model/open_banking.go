package model

import "encoding/json"

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
	LastSyncedAt       string `json:"last_synced_at,omitempty"`
}

type OpenBankingSyncResult struct {
	Fetched       int `json:"fetched"`
	Imported      int `json:"imported"`
	Updated       int `json:"updated"`
	Unchanged     int `json:"unchanged"`
	Ignored       int `json:"ignored"`
	Notifications int `json:"notifications"`
}

type OpenBankingMaintenanceResult struct {
	Claimed       int `json:"claimed"`
	Succeeded     int `json:"succeeded"`
	Failed        int `json:"failed"`
	Imported      int `json:"imported"`
	Updated       int `json:"updated"`
	Notifications int `json:"notifications"`
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

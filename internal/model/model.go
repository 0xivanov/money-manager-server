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
	ID                   int    `json:"id"`
	Type                 string `json:"type"`
	Category             string `json:"category"`
	Description          string `json:"description"`
	Amount               string `json:"amount"`
	Currency             string `json:"currency"`
	OccurredAt           string `json:"occurred_at"`
	Source               string `json:"source"`
	Status               string `json:"status"`
	ExcludedFromBudget   bool   `json:"excluded_from_budget"`
	ScheduleOccurrenceID *int   `json:"schedule_occurrence_id,omitempty"`
}
type TransactionRequest struct {
	Type               string `json:"type"`
	Category           string `json:"category"`
	Description        string `json:"description"`
	Amount             string `json:"amount"`
	Currency           string `json:"currency"`
	OccurredAt         string `json:"occurred_at"`
	ExcludedFromBudget bool   `json:"excluded_from_budget"`
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

type TransactionSchedule struct {
	ID                  int    `json:"id"`
	UserID              int    `json:"-"`
	Type                string `json:"type"`
	Name                string `json:"name"`
	Category            string `json:"category"`
	Description         string `json:"description"`
	Amount              string `json:"amount"`
	Currency            string `json:"currency"`
	Frequency           string `json:"frequency"`
	FrequencyInterval   int    `json:"frequency_interval"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date,omitempty"`
	DayOfWeek           *int   `json:"day_of_week,omitempty"`
	DayOfMonth          *int   `json:"day_of_month,omitempty"`
	Timezone            string `json:"timezone"`
	AutoPost            bool   `json:"auto_post"`
	Status              string `json:"status"`
	MaterializedThrough string `json:"materialized_through,omitempty"`
	NextOccurrenceDate  string `json:"next_occurrence_date,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

type TransactionScheduleRequest struct {
	Type              string `json:"type"`
	Name              string `json:"name"`
	Category          string `json:"category"`
	Description       string `json:"description"`
	Amount            string `json:"amount"`
	Currency          string `json:"currency"`
	Frequency         string `json:"frequency"`
	FrequencyInterval int    `json:"frequency_interval,omitempty"`
	StartDate         string `json:"start_date"`
	EndDate           string `json:"end_date,omitempty"`
	DayOfWeek         *int   `json:"day_of_week,omitempty"`
	DayOfMonth        *int   `json:"day_of_month,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	AutoPost          bool   `json:"auto_post"`
}

type TransactionScheduleOccurrence struct {
	ID            int    `json:"id"`
	ScheduleID    int    `json:"schedule_id"`
	ScheduledFor  string `json:"scheduled_for"`
	Status        string `json:"status"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	AutoPost      bool   `json:"auto_post"`
	TransactionID *int   `json:"transaction_id,omitempty"`
}

type ScheduleMaintenanceResult struct {
	Materialized        int `json:"materialized"`
	Posted              int `json:"posted"`
	ScheduleReminders   int `json:"schedule_reminders"`
	BudgetAlerts        int `json:"budget_alerts"`
	InvestmentReminders int `json:"investment_reminders"`
}

type Budget struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Category         string `json:"category,omitempty"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	Period           string `json:"period"`
	WarningThreshold int    `json:"warning_threshold"`
	Status           string `json:"status"`
	PeriodStart      string `json:"period_start"`
	PeriodEnd        string `json:"period_end"`
	SpentAmount      string `json:"spent_amount"`
	RemainingAmount  string `json:"remaining_amount"`
	ProgressPercent  string `json:"progress_percent"`
	AlertLevel       string `json:"alert_level"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type BudgetRequest struct {
	Name             string `json:"name"`
	Category         string `json:"category,omitempty"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency,omitempty"`
	Period           string `json:"period"`
	WarningThreshold int    `json:"warning_threshold,omitempty"`
}

type NotificationPreferences struct {
	BankSpending        bool   `json:"bank_spending"`
	BudgetAlerts        bool   `json:"budget_alerts"`
	ScheduledMoney      bool   `json:"scheduled_money"`
	InvestmentReminders bool   `json:"investment_reminders"`
	QuietHoursStart     string `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd       string `json:"quiet_hours_end,omitempty"`
	Timezone            string `json:"timezone"`
}

type PushDevice struct {
	ID          int    `json:"id"`
	Platform    string `json:"platform"`
	DeviceToken string `json:"device_token,omitempty"`
	AppID       string `json:"app_id"`
	Environment string `json:"environment"`
	LastSeenAt  string `json:"last_seen_at"`
}

type NotificationDeliveryResult struct {
	Claimed     int `json:"claimed"`
	Sent        int `json:"sent"`
	Retrying    int `json:"retrying"`
	Dead        int `json:"dead"`
	Deactivated int `json:"deactivated"`
}

type PushDeviceRequest struct {
	Platform    string `json:"platform"`
	DeviceToken string `json:"device_token"`
	AppID       string `json:"app_id"`
	Environment string `json:"environment"`
}

type InvestmentTrade struct {
	ID           int    `json:"id"`
	AssetType    string `json:"asset_type"`
	Symbol       string `json:"symbol"`
	AssetName    string `json:"asset_name"`
	Broker       string `json:"broker"`
	Side         string `json:"side"`
	Quantity     string `json:"quantity"`
	PricePerUnit string `json:"price_per_unit"`
	Fees         string `json:"fees"`
	Currency     string `json:"currency"`
	OccurredAt   string `json:"occurred_at"`
	Notes        string `json:"notes"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type InvestmentTradeRequest struct {
	AssetType    string `json:"asset_type"`
	Symbol       string `json:"symbol"`
	AssetName    string `json:"asset_name"`
	Broker       string `json:"broker"`
	Side         string `json:"side"`
	Quantity     string `json:"quantity"`
	PricePerUnit string `json:"price_per_unit"`
	Fees         string `json:"fees,omitempty"`
	Currency     string `json:"currency,omitempty"`
	OccurredAt   string `json:"occurred_at"`
	Notes        string `json:"notes,omitempty"`
}

type InvestmentPrice struct {
	AssetType string `json:"asset_type"`
	Symbol    string `json:"symbol"`
	Currency  string `json:"currency"`
	Price     string `json:"price"`
	Provider  string `json:"provider"`
	AsOf      string `json:"as_of"`
}

type InvestmentPriceRequest struct {
	AssetType string `json:"asset_type"`
	Symbol    string `json:"symbol"`
	Currency  string `json:"currency,omitempty"`
	Price     string `json:"price"`
	AsOf      string `json:"as_of,omitempty"`
}

type InvestmentPosition struct {
	AssetType        string `json:"asset_type"`
	Symbol           string `json:"symbol"`
	AssetName        string `json:"asset_name"`
	Broker           string `json:"broker"`
	Quantity         string `json:"quantity"`
	AverageCost      string `json:"average_cost"`
	InvestedAmount   string `json:"invested_amount"`
	CurrentPrice     string `json:"current_price,omitempty"`
	CurrentValue     string `json:"current_value,omitempty"`
	UnrealizedProfit string `json:"unrealized_profit,omitempty"`
	UnrealizedPct    string `json:"unrealized_percent,omitempty"`
	RealizedProfit   string `json:"realized_profit"`
	Currency         string `json:"currency"`
	PriceAsOf        string `json:"price_as_of,omitempty"`
	PriceStatus      string `json:"price_status"`
}

type InvestmentPortfolio struct {
	Positions        []InvestmentPosition `json:"positions"`
	InvestedAmount   string               `json:"invested_amount"`
	CurrentValue     string               `json:"current_value,omitempty"`
	UnrealizedProfit string               `json:"unrealized_profit,omitempty"`
	RealizedProfit   string               `json:"realized_profit"`
	Currency         string               `json:"currency"`
	MissingPrices    int                  `json:"missing_prices"`
}

type InvestmentSchedule struct {
	ID                int    `json:"id"`
	UserID            int    `json:"-"`
	AssetType         string `json:"asset_type"`
	Symbol            string `json:"symbol"`
	AssetName         string `json:"asset_name"`
	Broker            string `json:"broker"`
	Amount            string `json:"amount"`
	Currency          string `json:"currency"`
	Frequency         string `json:"frequency"`
	FrequencyInterval int    `json:"frequency_interval"`
	StartDate         string `json:"start_date"`
	EndDate           string `json:"end_date,omitempty"`
	DayOfWeek         *int   `json:"day_of_week,omitempty"`
	DayOfMonth        *int   `json:"day_of_month,omitempty"`
	Timezone          string `json:"timezone"`
	Status            string `json:"status"`
	LastNotifiedOn    string `json:"last_notified_on,omitempty"`
	NextOccurrence    string `json:"next_occurrence,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

type InvestmentScheduleRequest struct {
	AssetType         string `json:"asset_type"`
	Symbol            string `json:"symbol"`
	AssetName         string `json:"asset_name"`
	Broker            string `json:"broker"`
	Amount            string `json:"amount"`
	Currency          string `json:"currency,omitempty"`
	Frequency         string `json:"frequency"`
	FrequencyInterval int    `json:"frequency_interval,omitempty"`
	StartDate         string `json:"start_date"`
	EndDate           string `json:"end_date,omitempty"`
	DayOfWeek         *int   `json:"day_of_week,omitempty"`
	DayOfMonth        *int   `json:"day_of_month,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
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

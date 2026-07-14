package model

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

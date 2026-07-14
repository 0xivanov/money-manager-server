package model

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

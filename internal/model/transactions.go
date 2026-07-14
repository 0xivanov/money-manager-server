package model

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

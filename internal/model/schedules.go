package model

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

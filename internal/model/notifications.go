package model

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

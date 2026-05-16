package model

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

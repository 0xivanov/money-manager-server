package api

import (
	"database/sql"
	"time"
)

type User struct {
	ID             int       `json:"id"`
	Username       string    `json:"username"`
	HashedPassword string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type Spending struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	Category  string    `json:"category"`
	Amount    float64   `json:"amount"`
	Date      time.Time `json:"date"`
	CreatedAt time.Time `json:"created_at"`
}

type Income struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	Category  string    `json:"category"`
	Amount    float64   `json:"amount"`
	Date      time.Time `json:"date"`
	CreatedAt time.Time `json:"created_at"`
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.HashedPassword, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func scanSpending(row *sql.Row) (*Spending, error) {
	var s Spending
	var dateStr string
	err := row.Scan(&s.ID, &s.UserID, &s.Category, &s.Amount, &dateStr, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	s.Date, err = time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func scanIncome(row *sql.Row) (*Income, error) {
	var i Income
	var dateStr string
	err := row.Scan(&i.ID, &i.UserID, &i.Category, &i.Amount, &dateStr, &i.CreatedAt)
	if err != nil {
		return nil, err
	}
	i.Date, err = time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

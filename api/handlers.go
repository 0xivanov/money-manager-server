package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xivanov/money-manager/db"
	"golang.org/x/crypto/bcrypt"
)

func CreateUser(w http.ResponseWriter, r *http.Request) {
	var user struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if user.Username == "" || user.Password == "" {
		http.Error(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to hash password: %v", err), http.StatusInternalServerError)
		return
	}

	var id int
	err = db.DB.QueryRow(
		"INSERT INTO users (username, hashed_password) VALUES ($1, $2) RETURNING id",
		user.Username, string(hashedPassword),
	).Scan(&id)

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create user: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "username": user.Username})
}

func GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	user, err := scanUser(db.DB.QueryRow("SELECT id, username, hashed_password, created_at FROM users WHERE id = $1", id))
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func GetAllUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query("SELECT id, username, hashed_password, created_at FROM users")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch users: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.HashedPassword, &u.CreatedAt); err != nil {
			http.Error(w, fmt.Sprintf("Failed to scan user: %v", err), http.StatusInternalServerError)
			return
		}
		users = append(users, u)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var user struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var hashedPassword string
	if user.Password != "" {
		hashedBytes, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to hash password: %v", err), http.StatusInternalServerError)
			return
		}
		hashedPassword = string(hashedBytes)
	} else {
		var existingHash string
		err := db.DB.QueryRow("SELECT hashed_password FROM users WHERE id = $1", id).Scan(&existingHash)
		if err != nil {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		hashedPassword = existingHash
	}

	result, err := db.DB.Exec(
		"UPDATE users SET username = $1, hashed_password = $2 WHERE id = $3",
		user.Username, hashedPassword, id,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to update user: %v", err), http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "User updated successfully"})
}

func DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	result, err := db.DB.Exec("DELETE FROM users WHERE id = $1", id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete user: %v", err), http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "User deleted successfully"})
}

func CreateSpending(w http.ResponseWriter, r *http.Request) {
	var spending struct {
		UserID   int     `json:"user_id"`
		Category string  `json:"category"`
		Amount   float64 `json:"amount"`
		Date     string  `json:"date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&spending); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	date, err := time.Parse("2006-01-02", spending.Date)
	if err != nil {
		http.Error(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	var id int
	err = db.DB.QueryRow(
		"INSERT INTO spending (user_id, category, amount, date) VALUES ($1, $2, $3, $4) RETURNING id",
		spending.UserID, spending.Category, spending.Amount, date,
	).Scan(&id)

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create spending: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "message": "Spending created successfully"})
}

func GetSpending(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid spending ID", http.StatusBadRequest)
		return
	}

	spending, err := scanSpending(db.DB.QueryRow(
		"SELECT id, user_id, category, amount, date, created_at FROM spending WHERE id = $1", id,
	))
	if err != nil {
		http.Error(w, "Spending not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spending)
}

func GetAllSpending(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	query := "SELECT id, user_id, category, amount, date, created_at FROM spending"
	var rows *sql.Rows
	var err error

	if userID != "" {
		uid, err := strconv.Atoi(userID)
		if err != nil {
			http.Error(w, "Invalid user_id parameter", http.StatusBadRequest)
			return
		}
		rows, err = db.DB.Query(query+" WHERE user_id = $1 ORDER BY date DESC", uid)
	} else {
		rows, err = db.DB.Query(query + " ORDER BY date DESC")
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch spending: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var spendings []Spending
	for rows.Next() {
		var s Spending
		var dateStr string
		if err := rows.Scan(&s.ID, &s.UserID, &s.Category, &s.Amount, &dateStr, &s.CreatedAt); err != nil {
			http.Error(w, fmt.Sprintf("Failed to scan spending: %v", err), http.StatusInternalServerError)
			return
		}
		s.Date, _ = time.Parse("2006-01-02", dateStr)
		spendings = append(spendings, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spendings)
}

func UpdateSpending(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid spending ID", http.StatusBadRequest)
		return
	}

	var spending struct {
		Category string  `json:"category"`
		Amount   float64 `json:"amount"`
		Date     string  `json:"date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&spending); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	date, err := time.Parse("2006-01-02", spending.Date)
	if err != nil {
		http.Error(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	result, err := db.DB.Exec(
		"UPDATE spending SET category = $1, amount = $2, date = $3 WHERE id = $4",
		spending.Category, spending.Amount, date, id,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to update spending: %v", err), http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Spending not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Spending updated successfully"})
}

func DeleteSpending(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid spending ID", http.StatusBadRequest)
		return
	}

	result, err := db.DB.Exec("DELETE FROM spending WHERE id = $1", id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete spending: %v", err), http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Spending not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Spending deleted successfully"})
}

func CreateIncome(w http.ResponseWriter, r *http.Request) {
	var income struct {
		UserID   int     `json:"user_id"`
		Category string  `json:"category"`
		Amount   float64 `json:"amount"`
		Date     string  `json:"date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&income); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	date, err := time.Parse("2006-01-02", income.Date)
	if err != nil {
		http.Error(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	var id int
	err = db.DB.QueryRow(
		"INSERT INTO income (user_id, category, amount, date) VALUES ($1, $2, $3, $4) RETURNING id",
		income.UserID, income.Category, income.Amount, date,
	).Scan(&id)

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create income: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "message": "Income created successfully"})
}

func GetIncome(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid income ID", http.StatusBadRequest)
		return
	}

	income, err := scanIncome(db.DB.QueryRow(
		"SELECT id, user_id, category, amount, date, created_at FROM income WHERE id = $1", id,
	))
	if err != nil {
		http.Error(w, "Income not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(income)
}

func GetAllIncome(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	query := "SELECT id, user_id, category, amount, date, created_at FROM income"
	var rows *sql.Rows
	var err error

	if userID != "" {
		uid, err := strconv.Atoi(userID)
		if err != nil {
			http.Error(w, "Invalid user_id parameter", http.StatusBadRequest)
			return
		}
		rows, err = db.DB.Query(query+" WHERE user_id = $1 ORDER BY date DESC", uid)
	} else {
		rows, err = db.DB.Query(query + " ORDER BY date DESC")
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch income: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var incomes []Income
	for rows.Next() {
		var i Income
		var dateStr string
		if err := rows.Scan(&i.ID, &i.UserID, &i.Category, &i.Amount, &dateStr, &i.CreatedAt); err != nil {
			http.Error(w, fmt.Sprintf("Failed to scan income: %v", err), http.StatusInternalServerError)
			return
		}
		i.Date, _ = time.Parse("2006-01-02", dateStr)
		incomes = append(incomes, i)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(incomes)
}

func UpdateIncome(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid income ID", http.StatusBadRequest)
		return
	}

	var income struct {
		Category string  `json:"category"`
		Amount   float64 `json:"amount"`
		Date     string  `json:"date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&income); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	date, err := time.Parse("2006-01-02", income.Date)
	if err != nil {
		http.Error(w, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	result, err := db.DB.Exec(
		"UPDATE income SET category = $1, amount = $2, date = $3 WHERE id = $4",
		income.Category, income.Amount, date, id,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to update income: %v", err), http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Income not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Income updated successfully"})
}

func DeleteIncome(w http.ResponseWriter, r *http.Request) {
	id, err := getPathID(r, "id")
	if err != nil {
		http.Error(w, "Invalid income ID", http.StatusBadRequest)
		return
	}

	result, err := db.DB.Exec("DELETE FROM income WHERE id = $1", id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete income: %v", err), http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Income not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Income deleted successfully"})
}

func getPathID(r *http.Request, key string) (int, error) {
	idStr := r.PathValue(key)
	if idStr == "" {
		return extractID(r.URL.Path, "")
	}
	return strconv.Atoi(idStr)
}

func extractID(path, prefix string) (int, error) {
	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return 0, fmt.Errorf("invalid path")
	}
	if prefix != "" {
		path = strings.TrimPrefix(path, prefix)
	}
	idStr := strings.Split(path, "/")[0]
	return strconv.Atoi(idStr)
}

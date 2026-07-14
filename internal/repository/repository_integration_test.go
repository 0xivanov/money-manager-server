package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"money-manager-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepositoryIntegration(t *testing.T) {
	ctx, repo, pool := openIntegrationRepository(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}

	user, err := repo.RegisterUser(ctx, "person@example.com", "hash")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	if _, err := repo.RegisterUser(ctx, "PERSON@example.com", "hash"); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}
	record, err := repo.FindUserByEmail(ctx, "PERSON@example.com")
	if err != nil || record.User.ID != user.ID {
		t.Fatalf("find user = %#v, %v", record, err)
	}
	categories, err := repo.ListCategories(ctx, user.ID, "expense")
	if err != nil || len(categories) != 10 {
		t.Fatalf("expense categories = %d, %v", len(categories), err)
	}
	category, err := repo.FindActiveCategoryName(ctx, user.ID, "expense", "FOOD")
	if err != nil || category != "food" {
		t.Fatalf("find category = %q, %v", category, err)
	}

	transaction, err := repo.CreateTransaction(ctx, user.ID, model.TransactionRequest{
		Type: "expense", Category: category, Description: "Lunch", Amount: "12.50",
		Currency: "EUR", OccurredAt: "2026-07-11",
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	monthStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	transactions, err := repo.ListTransactions(ctx, user.ID, TransactionFilter{From: monthStart, To: monthStart.AddDate(0, 1, 0)})
	if err != nil || len(transactions) != 1 || transactions[0].ID != transaction.ID {
		t.Fatalf("list transactions = %#v, %v", transactions, err)
	}
	for range 2 {
		if _, err := repo.CreateTransaction(ctx, user.ID, model.TransactionRequest{
			Type: "income", Category: "salary", Amount: "999999999999.99", Currency: "EUR", OccurredAt: "2026-07-11",
		}); err != nil {
			t.Fatalf("create maximum income transaction: %v", err)
		}
	}
	summary, err := repo.Summary(ctx, user.ID, "2026-07", monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil || summary.Income != "1999999999999.98" || summary.Expense != "12.50" || summary.Balance != "1999999999987.48" {
		t.Fatalf("summary = %#v, %v", summary, err)
	}
	if _, err := repo.CreateTransaction(ctx, user.ID, model.TransactionRequest{
		Type: "expense", Category: "food", Amount: "1.00", Currency: "USD", OccurredAt: "2026-07-11",
	}); err == nil {
		t.Fatal("database accepted unsupported currency")
	}
	if err := repo.DeleteTransaction(ctx, user.ID, transaction.ID); err != nil {
		t.Fatalf("delete transaction: %v", err)
	}
	if err := repo.DeleteTransaction(ctx, user.ID, transaction.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}

	validUntil := time.Now().UTC().Add(30 * 24 * time.Hour)
	authorizationID, err := repo.CreateOpenBankingAuthorization(ctx, NewOpenBankingAuthorization{
		UserID: user.ID, StateHash: strings.Repeat("a", 64), InstitutionName: "Revolut",
		Country: "BG", PSUType: "personal", ValidUntil: validUntil, ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create open banking authorization: %v", err)
	}
	if err := repo.SetOpenBankingAuthorizationProviderID(ctx, authorizationID, "provider-authorization"); err != nil {
		t.Fatalf("set provider authorization ID: %v", err)
	}
	claimed, err := repo.ClaimOpenBankingAuthorization(ctx, strings.Repeat("a", 64), time.Now().UTC())
	if err != nil || claimed.UserID != user.ID {
		t.Fatalf("claim open banking authorization = %#v, %v", claimed, err)
	}
	if _, err := repo.ClaimOpenBankingAuthorization(ctx, strings.Repeat("a", 64), time.Now().UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replayed open banking state error = %v", err)
	}
	connectionID, err := repo.StoreOpenBankingConnection(ctx, NewOpenBankingConnection{
		AuthorizationID: authorizationID, UserID: user.ID, ProviderSession: "provider-session",
		InstitutionName: "Revolut", Country: "BG", PSUType: "personal", Status: "AUTHORIZED",
		ValidUntil: validUntil, Accounts: []NewOpenBankingAccount{{
			ProviderAccountID: "provider-account", IdentificationHash: "stable-hash",
			Name: "Everyday", CashAccountType: "CACC", Currency: "EUR",
			DisplayIdentifier: "•••• 0123", ProviderPayload: []byte(`{"uid":"provider-account"}`),
		}},
	})
	if err != nil {
		t.Fatalf("store open banking connection: %v", err)
	}
	connections, err := repo.ListOpenBankingConnections(ctx, user.ID)
	if err != nil || len(connections) != 1 || connections[0].ID != connectionID || connections[0].AccountCount != 1 {
		t.Fatalf("open banking connections = %#v, %v", connections, err)
	}
	accounts, err := repo.ListOpenBankingAccounts(ctx, user.ID)
	if err != nil || len(accounts) != 1 || accounts[0].DisplayIdentifier != "•••• 0123" || !accounts[0].CanFetchData {
		t.Fatalf("open banking accounts = %#v, %v", accounts, err)
	}
	accountID := accounts[0].ID
	claimTime := time.Now().UTC().Add(time.Second)
	syncClaimed, err := repo.ClaimOpenBankingAccountsForSync(
		ctx, claimTime, claimTime.Add(6*time.Hour), claimTime.Add(5*time.Minute), 10,
	)
	if err != nil || len(syncClaimed) != 1 || syncClaimed[0].UserID != user.ID || syncClaimed[0].AccountID != accountID {
		t.Fatalf("claimed open banking accounts = %#v, %v", syncClaimed, err)
	}
	claimedAgain, err := repo.ClaimOpenBankingAccountsForSync(
		ctx, claimTime, claimTime.Add(6*time.Hour), claimTime.Add(5*time.Minute), 10,
	)
	if err != nil || len(claimedAgain) != 0 {
		t.Fatalf("duplicate open banking claims = %#v, %v", claimedAgain, err)
	}
	firstSync, err := repo.ImportOpenBankingTransactions(ctx, user.ID, accountID, []OpenBankingTransactionSeed{{
		ExternalID: "bank-transaction-1", Type: "expense", Category: "food",
		Description: "Fresh Market", Amount: "42.80", Currency: "EUR",
		OccurredAt: monthStart.AddDate(0, 0, 10), Status: "booked", Metadata: []byte(`{"mcc":"5411"}`),
	}}, claimTime)
	if err != nil || firstSync.Imported != 1 || firstSync.Notifications != 0 {
		t.Fatalf("initial bank sync = %#v, %v", firstSync, err)
	}
	secondSync, err := repo.ImportOpenBankingTransactions(ctx, user.ID, accountID, []OpenBankingTransactionSeed{
		{
			ExternalID: "bank-transaction-1", Type: "expense", Category: "food",
			Description: "Fresh Market", Amount: "42.80", Currency: "EUR",
			OccurredAt: monthStart.AddDate(0, 0, 10), Status: "booked", Metadata: []byte(`{"mcc":"5411"}`),
		},
		{
			ExternalID: "bank-transaction-2", Type: "expense", Category: "transport",
			Description: "Metro", Amount: "2.00", Currency: "EUR",
			OccurredAt: monthStart.AddDate(0, 0, 11), Status: "booked", Metadata: []byte(`{"mcc":"4111"}`),
		},
	}, claimTime)
	if err != nil || secondSync.Imported != 1 || secondSync.Unchanged != 1 || secondSync.Notifications != 1 {
		t.Fatalf("incremental bank sync = %#v, %v", secondSync, err)
	}
	var bankTransactions, bankNotifications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions
		WHERE user_id=$1 AND source='open_banking'`, user.ID).Scan(&bankTransactions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox
		WHERE user_id=$1 AND event_type='bank_spending'`, user.ID).Scan(&bankNotifications); err != nil {
		t.Fatal(err)
	}
	if bankTransactions != 2 || bankNotifications != 1 {
		t.Fatalf("persisted bank sync transactions=%d notifications=%d", bankTransactions, bankNotifications)
	}
	scheduleReminders, err := repo.QueueDueTransactionScheduleReminders(ctx, claimTime, 10)
	if err != nil || scheduleReminders != 0 {
		t.Fatalf("queue due transaction schedule reminders = %d, %v", scheduleReminders, err)
	}
	device, err := repo.RegisterPushDevice(ctx, user.ID, model.PushDeviceRequest{
		Platform: "ios", DeviceToken: "0123456789abcdef0123456789abcdef",
		AppID: "org.moneymanager.ios", Environment: "sandbox",
	})
	if err != nil {
		t.Fatalf("register push device: %v", err)
	}
	deliveryTime := claimTime.Add(time.Minute)
	deliveries, err := repo.ClaimNotificationDeliveries(
		ctx, deliveryTime, deliveryTime.Add(-10*time.Minute), []string{"ios"}, 10,
	)
	if err != nil || len(deliveries) != 1 || deliveries[0].DeviceID != device.ID || deliveries[0].EventType != "bank_spending" {
		t.Fatalf("claimed notification deliveries = %#v, %v", deliveries, err)
	}
	if err := repo.CompleteNotificationDelivery(
		ctx, deliveries[0].ID, true, false, false, "", deliveryTime, deliveryTime,
	); err != nil {
		t.Fatalf("complete notification delivery: %v", err)
	}
	var outboxStatus, deliveryStatus string
	if err := pool.QueryRow(ctx, `SELECT notification.status,delivery.status
		FROM notification_outbox notification
		JOIN notification_deliveries delivery ON delivery.notification_id=notification.id`).Scan(
		&outboxStatus, &deliveryStatus,
	); err != nil || outboxStatus != "sent" || deliveryStatus != "sent" {
		t.Fatalf("notification statuses = %q/%q, %v", outboxStatus, deliveryStatus, err)
	}
	claimedAfterInterval, err := repo.ClaimOpenBankingAccountsForSync(
		ctx, claimTime.Add(6*time.Hour+time.Second), claimTime.Add(12*time.Hour), claimTime.Add(6*time.Hour+5*time.Minute), 10,
	)
	if err != nil || len(claimedAfterInterval) != 1 || claimedAfterInterval[0].AccountID != accountID {
		t.Fatalf("open banking claim after interval = %#v, %v", claimedAfterInterval, err)
	}
}

func TestLegacySchemaUpgradeQuarantinesInvalidRows(t *testing.T) {
	ctx, repo, pool := openIntegrationRepository(t)
	_, err := pool.Exec(ctx, `
		CREATE TABLE users(id SERIAL PRIMARY KEY,email TEXT UNIQUE NOT NULL,password_hash TEXT NOT NULL);
		CREATE TABLE transactions(
			id SERIAL PRIMARY KEY,
			user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			category TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			amount NUMERIC(14,2) NOT NULL,
			currency TEXT NOT NULL,
			occurred_at DATE NOT NULL
		);
		CREATE TABLE categories(
			id SERIAL PRIMARY KEY,
			user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			is_default BOOLEAN NOT NULL DEFAULT false,
			active BOOLEAN NOT NULL DEFAULT true,
			sort_order INT NOT NULL DEFAULT 1000
		);
		INSERT INTO users(id,email,password_hash) VALUES
			(1,' Valid@Example.com ','hash'),
			(2,'valid@example.com','hash'),
			(3,E'bad\nemail','hash');
		INSERT INTO categories(id,user_id,type,name,is_default,active,sort_order) VALUES
			(1,1,' Expense ',' Food ',true,true,1),
			(2,1,'expense','food',false,true,2),
			(3,1,'unsupported','broken',false,true,3),
			(4,2,'expense','food',true,true,1);
		INSERT INTO transactions(id,user_id,type,category,description,amount,currency,occurred_at) VALUES
			(1,1,' Expense ',' Food ','valid',12.50,' eur ','2026-07-11'),
			(2,1,'expense','food','foreign currency',8.00,'USD','2026-07-11'),
			(3,2,'expense','food','duplicate user data',4.00,'EUR','2026-07-11'),
			(4,1,'expense','food','negative amount',-2.00,'EUR','2026-07-11');
	`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("upgrade legacy schema: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("repeat legacy migration: %v", err)
	}

	var users, categories, transactions, quarantined, versions int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM categories").Scan(&categories); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transactions").Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM migration_quarantine WHERE row_data IS NOT NULL").Scan(&quarantined); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if users != 1 || categories != 1 || transactions != 1 || quarantined != 8 || versions != 9 {
		t.Fatalf("legacy upgrade counts users=%d categories=%d transactions=%d quarantined=%d versions=%d", users, categories, transactions, quarantined, versions)
	}
	var email, transactionType, category, currency string
	if err := pool.QueryRow(ctx, "SELECT email FROM users").Scan(&email); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT type,category,currency FROM transactions").Scan(&transactionType, &category, &currency); err != nil {
		t.Fatal(err)
	}
	if email != "valid@example.com" || transactionType != "expense" || category != "Food" || currency != "EUR" {
		t.Fatalf("legacy values were not normalized safely: email=%q type=%q category=%q currency=%q", email, transactionType, category, currency)
	}
	if _, err := repo.CreateTransaction(ctx, 1, model.TransactionRequest{
		Type: "expense", Category: "Food", Amount: "1.00", Currency: "USD", OccurredAt: "2026-07-11",
	}); err == nil {
		t.Fatal("hardened constraint accepted USD after legacy upgrade")
	}
}

func openIntegrationRepository(t *testing.T) (context.Context, *Repository, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("money_manager_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })

	parsedURL, err := url.Parse(databaseURL)
	if err != nil || parsedURL.Scheme == "" {
		t.Fatalf("TEST_DATABASE_URL must be a postgres URL: %v", err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	pool, err := Open(ctx, parsedURL.String(), Options{
		MaxConns: 4, MinConns: 0, MaxConnLifetime: time.Minute,
		MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Minute,
	})
	if err != nil {
		t.Fatalf("open repository database: %v", err)
	}
	repo := New(pool)
	t.Cleanup(repo.Close)
	return ctx, repo, pool
}

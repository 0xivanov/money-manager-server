package repository

import (
	"context"
	"encoding/json"
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
	if err != nil || len(categories) != 14 {
		t.Fatalf("expense categories = %d, %v", len(categories), err)
	}
	category, err := repo.FindActiveCategoryName(ctx, user.ID, "expense", "GROCERIES")
	if err != nil || category != "groceries" {
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
	importSeed := model.ImportedTransaction{
		Request: model.TransactionRequest{
			Type: "expense", Category: "other", Description: "Imported shop", Amount: "9.50",
			Currency: "EUR", OccurredAt: "2026-07-12",
		},
		Fingerprint: "classifier-fingerprint",
	}
	if imported, skipped, err := repo.ImportTransactions(ctx, user.ID, []model.ImportedTransaction{importSeed}); err != nil || imported != 1 || skipped != 0 {
		t.Fatalf("initial import = %d/%d, %v", imported, skipped, err)
	}
	importSeed.Request.Category = "shopping"
	if imported, skipped, err := repo.ImportTransactions(ctx, user.ID, []model.ImportedTransaction{importSeed}); err != nil || imported != 0 || skipped != 1 {
		t.Fatalf("classified duplicate import = %d/%d, %v", imported, skipped, err)
	}
	transactions, err = repo.ListTransactions(ctx, user.ID, TransactionFilter{From: monthStart, To: monthStart.AddDate(0, 1, 0)})
	if err != nil {
		t.Fatalf("list imported transactions: %v", err)
	}
	var classifiedImport *model.Transaction
	for index := range transactions {
		if transactions[index].Description == "Imported shop" {
			classifiedImport = &transactions[index]
			break
		}
	}
	if classifiedImport == nil || classifiedImport.Category != "shopping" {
		t.Fatalf("classified duplicate import = %#v", classifiedImport)
	}
	for range 2 {
		if _, err := repo.CreateTransaction(ctx, user.ID, model.TransactionRequest{
			Type: "income", Category: "salary", Amount: "999999999999.99", Currency: "EUR", OccurredAt: "2026-07-11",
		}); err != nil {
			t.Fatalf("create maximum income transaction: %v", err)
		}
	}
	summary, err := repo.Summary(ctx, user.ID, "2026-07", monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil || summary.Income != "1999999999999.98" || summary.Expense != "22.00" || summary.Balance != "1999999999977.98" {
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

	tradeTime := time.Date(2026, 7, 11, 14, 30, 0, 0, time.UTC)
	trade, err := repo.CreateInvestmentTrade(ctx, user.ID, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual", Side: "buy",
		Amount: "100.00", Quantity: "0.002", PricePerUnit: "50000", PriceProvider: "kraken",
		PriceAsOf: tradeTime.Add(-time.Second).Format(time.RFC3339), Fees: "1.00", Currency: "EUR",
		OccurredAt: tradeTime.Format(time.RFC3339), Notes: "integration buy",
	})
	if err != nil || trade.Amount != "100.00" || trade.Quantity != "0.002000000000000000" ||
		trade.PriceProvider != "kraken" || trade.OccurredAt != tradeTime.Format(time.RFC3339) {
		t.Fatalf("create investment trade = %#v, %v", trade, err)
	}
	investmentTrades, err := repo.ListInvestmentTrades(ctx, user.ID, InvestmentTradeFilter{
		From:    time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC),
		Through: time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC),
	})
	if err != nil || len(investmentTrades) != 1 || investmentTrades[0].ID != trade.ID {
		t.Fatalf("list investment trades = %#v, %v", investmentTrades, err)
	}
	_, err = repo.CreateInvestmentTrade(ctx, user.ID, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual", Side: "sell",
		Amount: "150.00", Quantity: "0.003", PricePerUnit: "50000", PriceProvider: "kraken",
		PriceAsOf: tradeTime.Add(time.Hour).Format(time.RFC3339), Fees: "0.00", Currency: "EUR",
		OccurredAt: tradeTime.Add(time.Hour).Format(time.RFC3339),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("oversized investment sale error = %v", err)
	}
	sale, err := repo.CreateInvestmentTrade(ctx, user.ID, model.InvestmentTradeRequest{
		AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual", Side: "sell",
		Amount: "50.00", Quantity: "0.001", PricePerUnit: "50000", PriceProvider: "kraken",
		PriceAsOf: tradeTime.Add(time.Hour).Format(time.RFC3339), Fees: "0.00", Currency: "EUR",
		OccurredAt: tradeTime.Add(time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("create valid investment sale: %v", err)
	}
	if err := repo.DeleteInvestmentTrade(ctx, user.ID, trade.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete depended-on investment buy error = %v", err)
	}
	if holding, err := repo.InvestmentHoldingQuantity(ctx, user.ID, "crypto", "BTC", "", "manual"); err != nil || holding != "0.001000000000000000" {
		t.Fatalf("holding after rejected buy deletion = %q, %v", holding, err)
	}
	if err := repo.DeleteInvestmentTrade(ctx, user.ID, sale.ID); err != nil {
		t.Fatalf("delete investment sale: %v", err)
	}
	if err := repo.DeleteInvestmentTrade(ctx, user.ID, trade.ID); err != nil {
		t.Fatalf("delete investment buy: %v", err)
	}
	if err := repo.DeleteInvestmentTrade(ctx, user.ID, trade.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing investment trade error = %v", err)
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
	var bankTransactionID int
	if err := pool.QueryRow(ctx, `SELECT id FROM transactions
		WHERE user_id=$1 AND source_account_id=$2 AND external_id='bank-transaction-1'`,
		user.ID, accountID,
	).Scan(&bankTransactionID); err != nil {
		t.Fatalf("find bank transaction for override: %v", err)
	}
	if _, err := repo.UpdateTransaction(ctx, user.ID, bankTransactionID, model.TransactionRequest{
		Type: "income", Category: "gift", Description: "Manual correction", Amount: "42.80",
		Currency: "EUR", OccurredAt: "2026-07-11",
	}); err != nil {
		t.Fatalf("override bank transaction classification: %v", err)
	}
	var overrideMetadataJSON string
	if err := pool.QueryRow(ctx, `SELECT source_metadata::text FROM transactions WHERE id=$1`, bankTransactionID).Scan(
		&overrideMetadataJSON,
	); err != nil {
		t.Fatalf("read bank transaction override metadata: %v", err)
	}
	var overrideMetadata map[string]any
	if err := json.Unmarshal([]byte(overrideMetadataJSON), &overrideMetadata); err != nil {
		t.Fatalf("decode bank transaction override metadata: %v", err)
	}
	if overrideMetadata["classification_override"] != true || overrideMetadata["type_override"] != true ||
		overrideMetadata["category_override"] != true || overrideMetadata["category_source"] != "user_override" {
		t.Fatalf("bank transaction override metadata = %#v", overrideMetadata)
	}
	thirdSync, err := repo.ImportOpenBankingTransactions(ctx, user.ID, accountID, []OpenBankingTransactionSeed{{
		ExternalID: "bank-transaction-1", Type: "expense", Category: "transport",
		Description: "Updated BOLT ride", Amount: "43.00", Currency: "EUR",
		OccurredAt: monthStart.AddDate(0, 0, 10), Status: "booked",
		Metadata: []byte(`{
			"classification_source":"expense_keyword",
			"classified_category":"transport",
			"classified_type":"expense",
			"category_source":"expense_keyword"
		}`),
	}}, claimTime)
	if err != nil || thirdSync.Updated != 1 || thirdSync.Notifications != 0 {
		t.Fatalf("bank sync after manual classification override = %#v, %v", thirdSync, err)
	}
	var storedType, storedCategory, storedDescription, storedAmount, storedMetadataJSON string
	if err := pool.QueryRow(ctx, `SELECT type,category,description,amount::text,source_metadata::text
		FROM transactions WHERE id=$1`, bankTransactionID,
	).Scan(&storedType, &storedCategory, &storedDescription, &storedAmount, &storedMetadataJSON); err != nil {
		t.Fatalf("read bank transaction after re-sync: %v", err)
	}
	if storedType != "income" || storedCategory != "gift" || storedDescription != "Updated BOLT ride" || storedAmount != "43.00" {
		t.Fatalf("bank transaction after re-sync = %q/%q, %q, %q", storedType, storedCategory, storedDescription, storedAmount)
	}
	var storedMetadata map[string]any
	if err := json.Unmarshal([]byte(storedMetadataJSON), &storedMetadata); err != nil {
		t.Fatalf("decode re-synced bank transaction metadata: %v", err)
	}
	if storedMetadata["classification_source"] != "expense_keyword" ||
		storedMetadata["classified_category"] != "transport" ||
		storedMetadata["category_source"] != "user_override" ||
		storedMetadata["classification_override"] != true ||
		storedMetadata["type_override"] != true || storedMetadata["category_override"] != true {
		t.Fatalf("re-synced bank transaction metadata = %#v", storedMetadata)
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
	var suppressedTransactionID int
	if err := pool.QueryRow(ctx, `SELECT id FROM transactions
		WHERE user_id=$1 AND source_account_id=$2 AND external_id='bank-transaction-2'`,
		user.ID, accountID,
	).Scan(&suppressedTransactionID); err != nil {
		t.Fatalf("find bank transaction for deletion: %v", err)
	}
	if err := repo.DeleteTransaction(ctx, user.ID, suppressedTransactionID); err != nil {
		t.Fatalf("delete bank transaction: %v", err)
	}
	suppressedSync, err := repo.ImportOpenBankingTransactions(ctx, user.ID, accountID, []OpenBankingTransactionSeed{{
		ExternalID: "bank-transaction-2", Type: "expense", Category: "transport",
		Description: "Metro", Amount: "2.00", Currency: "EUR",
		OccurredAt: monthStart.AddDate(0, 0, 11), Status: "booked", Metadata: []byte(`{"mcc":"4111"}`),
	}}, claimTime)
	if err != nil || suppressedSync.Imported != 0 || suppressedSync.Updated != 0 || suppressedSync.Unchanged != 0 {
		t.Fatalf("sync after manual bank transaction deletion = %#v, %v", suppressedSync, err)
	}
	var reimportedTransactions, suppressions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions
		WHERE user_id=$1 AND source_account_id=$2 AND external_id='bank-transaction-2'`,
		user.ID, accountID,
	).Scan(&reimportedTransactions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM open_banking_transaction_suppressions
		WHERE user_id=$1 AND source_account_id=$2 AND external_id='bank-transaction-2'`,
		user.ID, accountID,
	).Scan(&suppressions); err != nil {
		t.Fatal(err)
	}
	if reimportedTransactions != 0 || suppressions != 1 {
		t.Fatalf("deleted bank transaction reimported=%d suppressions=%d", reimportedTransactions, suppressions)
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
	if _, err := pool.Exec(ctx, `INSERT INTO notification_outbox(
		user_id,event_type,event_key,title,body,payload,created_at,updated_at
	) VALUES($1,'budget_alert','expired-smoke','Expired','Do not deliver','{}',$2,$2)`,
		user.ID, deliveryTime.Add(-48*time.Hour)); err != nil {
		t.Fatalf("insert expired notification: %v", err)
	}
	deliveries, err := repo.ClaimNotificationDeliveries(
		ctx, deliveryTime, deliveryTime.Add(-10*time.Minute), deliveryTime.Add(-24*time.Hour), []string{"ios"}, 10,
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
		JOIN notification_deliveries delivery ON delivery.notification_id=notification.id
		WHERE notification.event_type='bank_spending'`).Scan(
		&outboxStatus, &deliveryStatus,
	); err != nil || outboxStatus != "sent" || deliveryStatus != "sent" {
		t.Fatalf("notification statuses = %q/%q, %v", outboxStatus, deliveryStatus, err)
	}
	var expiredStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE event_key='expired-smoke'`).Scan(
		&expiredStatus,
	); err != nil || expiredStatus != "dead" {
		t.Fatalf("expired notification status = %q, %v", expiredStatus, err)
	}
	claimedAfterInterval, err := repo.ClaimOpenBankingAccountsForSync(
		ctx, claimTime.Add(6*time.Hour+time.Second), claimTime.Add(12*time.Hour), claimTime.Add(6*time.Hour+5*time.Minute), 10,
	)
	if err != nil || len(claimedAfterInterval) != 1 || claimedAfterInterval[0].AccountID != accountID {
		t.Fatalf("open banking claim after interval = %#v, %v", claimedAfterInterval, err)
	}
	budget, err := repo.CreateBudget(ctx, user.ID, model.BudgetRequest{
		Name: "Shopping cap", Category: "shopping", Amount: "9.00", Currency: "EUR",
		Period: "monthly", WarningThreshold: 80,
	}, monthStart)
	if err != nil {
		t.Fatalf("create budget for alert: %v", err)
	}
	budgetAlerts, err := repo.QueueBudgetAlerts(ctx, monthStart.AddDate(0, 0, 12))
	if err != nil || budgetAlerts != 2 {
		t.Fatalf("queue budget alerts = %d, %v", budgetAlerts, err)
	}
	var budgetPayloads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox
		WHERE user_id=$1 AND event_type='budget_alert'
			AND payload @> jsonb_build_object('budget_id',$2::bigint)`,
		user.ID, budget.ID,
	).Scan(&budgetPayloads); err != nil || budgetPayloads != 2 {
		t.Fatalf("budget alert payloads = %d, %v", budgetPayloads, err)
	}
	investmentSchedule, err := repo.CreateInvestmentSchedule(ctx, user.ID, model.InvestmentScheduleRequest{
		AssetType: "crypto", Symbol: "BTC", AssetName: "Bitcoin", Broker: "manual",
		Amount: "25.00", Currency: "EUR", Frequency: "daily", FrequencyInterval: 1,
		StartDate: monthStart.Format("2006-01-02"), Timezone: "Europe/Sofia",
	})
	if err != nil {
		t.Fatalf("create investment schedule for reminder: %v", err)
	}
	queuedReminder, err := repo.QueueInvestmentReminder(ctx, investmentSchedule, monthStart)
	if err != nil || !queuedReminder {
		t.Fatalf("queue investment reminder = %t, %v", queuedReminder, err)
	}
	var reminderPayloads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox
		WHERE user_id=$1 AND event_type='investment_reminder'
			AND payload @> jsonb_build_object('investment_schedule_id',$2::bigint)`,
		user.ID, investmentSchedule.ID,
	).Scan(&reminderPayloads); err != nil || reminderPayloads != 1 {
		t.Fatalf("investment reminder payloads = %d, %v", reminderPayloads, err)
	}
}

func TestInvestmentTradeMigrationBackfillsMarketDataAuditFields(t *testing.T) {
	ctx, repo, pool := openIntegrationRepository(t, "Europe/Sofia")
	var sessionTimezone string
	if err := pool.QueryRow(ctx, `SHOW TIME ZONE`).Scan(&sessionTimezone); err != nil {
		t.Fatal(err)
	}
	if sessionTimezone != "UTC" {
		t.Fatalf("repository session timezone = %q", sessionTimezone)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("create migration table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations {
		if item.version >= 10 {
			break
		}
		if _, err := pool.Exec(ctx, item.sql); err != nil {
			t.Fatalf("apply migration %d: %v", item.version, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES($1,$2)`, item.version, item.name); err != nil {
			t.Fatalf("record migration %d: %v", item.version, err)
		}
	}

	user, err := repo.RegisterUser(ctx, "investor@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO investment_trades(
		user_id,asset_type,symbol,asset_name,broker,side,quantity,price_per_unit,
		fees,currency,occurred_at,notes
	) VALUES($1,'crypto','BTC','Bitcoin','manual','buy','0.0012345678','50000.12345678',
		'1.25','EUR','2026-07-11','legacy trade')`, user.ID); err != nil {
		t.Fatalf("insert legacy investment trade: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO investment_trades(
		user_id,asset_type,symbol,asset_name,broker,side,quantity,price_per_unit,
		fees,currency,occurred_at,notes
	) VALUES($1,'crypto','ETH','Ethereum','manual','buy','0.0000000001','0.01000000',
		'0','EUR','2026-07-10','sub-cent legacy trade')`, user.ID); err != nil {
		t.Fatalf("insert sub-cent legacy investment trade: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("upgrade investment trade schema: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("repeat investment trade migration: %v", err)
	}

	var amount, quantity, provider, priceAsOf, occurredAt string
	if err := pool.QueryRow(ctx, `SELECT amount::text,quantity::text,price_provider,
		to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM investment_trades WHERE user_id=$1 AND notes='legacy trade'`, user.ID,
	).Scan(&amount, &quantity, &provider, &priceAsOf, &occurredAt); err != nil {
		t.Fatal(err)
	}
	if amount != "61.728542415765279684" || quantity != "0.001234567800000000" || provider != "legacy_manual" ||
		priceAsOf != "2026-07-11T00:00:00Z" || occurredAt != "2026-07-11T00:00:00Z" {
		t.Fatalf("backfilled trade = amount %q, quantity %q, provider %q, price %q, occurred %q",
			amount, quantity, provider, priceAsOf, occurredAt)
	}
	if err := pool.QueryRow(ctx, `SELECT amount::text FROM investment_trades
		WHERE user_id=$1 AND notes='sub-cent legacy trade'`, user.ID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != "0.000000000001" {
		t.Fatalf("sub-cent legacy amount = %q", amount)
	}

	if err := pool.QueryRow(ctx, `INSERT INTO investment_trades(
		user_id,asset_type,symbol,asset_name,broker,side,quantity,price_per_unit,
		fees,currency,occurred_at,notes
	) VALUES($1,'crypto','BTC','Bitcoin','manual','buy','0.002','50000',
		'0','EUR','2026-07-12','old binary insert')
	RETURNING amount::text,price_provider,
		to_char(price_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`, user.ID,
	).Scan(&amount, &provider, &priceAsOf); err != nil {
		t.Fatalf("insert through legacy compatibility trigger: %v", err)
	}
	if amount != "100" || provider != "legacy_manual" || priceAsOf != "2026-07-12T00:00:00Z" {
		t.Fatalf("legacy compatibility insert = amount %q, provider %q, price %q", amount, provider, priceAsOf)
	}

	var dataType string
	var precision, scale int
	if err := pool.QueryRow(ctx, `SELECT data_type,numeric_precision,numeric_scale
		FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='investment_trades' AND column_name='quantity'`,
	).Scan(&dataType, &precision, &scale); err != nil {
		t.Fatal(err)
	}
	if dataType != "numeric" || precision != 38 || scale != 18 {
		t.Fatalf("quantity column = %s(%d,%d)", dataType, precision, scale)
	}
	var amountIsUnbounded bool
	if err := pool.QueryRow(ctx, `SELECT numeric_precision IS NULL AND numeric_scale IS NULL
		FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='investment_trades' AND column_name='amount'`,
	).Scan(&amountIsUnbounded); err != nil {
		t.Fatal(err)
	}
	if !amountIsUnbounded {
		t.Fatal("amount column does not preserve arbitrary legacy decimal scale")
	}
}

func TestOpenBankingCategoryMigrationBackfillsAndPreservesOverrides(t *testing.T) {
	ctx, repo, pool := openIntegrationRepository(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("create migration table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations {
		if item.version >= 11 {
			break
		}
		if _, err := pool.Exec(ctx, item.sql); err != nil {
			t.Fatalf("apply migration %d: %v", item.version, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES($1,$2)`, item.version, item.name); err != nil {
			t.Fatalf("record migration %d: %v", item.version, err)
		}
	}

	user, err := repo.RegisterUser(ctx, "category-backfill@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	var connectionID int
	if err := pool.QueryRow(ctx, `INSERT INTO open_banking_connections(
		user_id,provider_session_id,institution_name,country,psu_type,status,valid_until
	) VALUES($1,'backfill-session','Revolut','BG','personal','AUTHORIZED',now()+interval '30 days')
	RETURNING id`, user.ID).Scan(&connectionID); err != nil {
		t.Fatalf("insert open banking connection: %v", err)
	}
	var accountID int
	if err := pool.QueryRow(ctx, `INSERT INTO open_banking_accounts(
		connection_id,provider_account_id,identification_hash,name,cash_account_type,currency,provider_payload
	) VALUES($1,'backfill-provider-account','backfill-account','Everyday','CACC','EUR','{}')
	RETURNING id`, connectionID).Scan(&accountID); err != nil {
		t.Fatalf("insert open banking account: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO transactions(
		user_id,type,category,description,amount,currency,occurred_at,source,status,
		source_account_id,external_id,source_metadata
	) VALUES
		($1,'expense','other','Unknown merchant',18.90,'EUR','2026-07-10','open_banking','booked',$2,
			'mcc-food','{"merchant_category_code":"5411"}'),
		($1,'expense','Other','BOLT.EU ride',12.00,'EUR','2026-07-10','open_banking','booked',$2,
			'keyword-transport','{}'),
		($1,'income','other','July payroll',3200.00,'EUR','2026-07-10','open_banking','booked',$2,
			'income-salary','{}'),
		($1,'income','salary','Legacy monthly payroll',3200.00,'EUR','2026-07-10','open_banking','booked',$2,
			'legacy-auto-salary','{}'),
		($1,'income','refund','Card payment reversal',25.00,'EUR','2026-07-10','open_banking','booked',$2,
			'legacy-auto-refund','{}'),
		($1,'expense','food','Legacy grocery merchant',30.00,'EUR','2026-07-10','open_banking','booked',$2,
			'legacy-auto-food','{"merchant_category_code":"5411"}'),
		($1,'expense','shopping','Legacy department store',40.00,'EUR','2026-07-10','open_banking','booked',$2,
			'legacy-auto-shopping','{"merchant_category_code":"5311"}'),
		($1,'expense','gift','Manual correction',20.00,'EUR','2026-07-10','open_banking','booked',$2,
			'manual-gift','{}'),
		($1,'expense','other','LIDL purchase',30.00,'EUR','2026-07-10','open_banking','booked',$2,
			'override-other','{"classification_override":true,"category_override":true,"category_source":"user_override"}'),
		($1,'expense','food','Previously classified',40.00,'EUR','2026-07-10','open_banking','booked',$2,
			'automatic-food','{"classification_source":"mcc","classified_category":"food","classified_type":"expense","category_source":"mcc"}'),
		($1,'expense','other','Unrecognized payment',50.00,'EUR','2026-07-10','open_banking','booked',$2,
			'fallback-other','{}')`, user.ID, accountID); err != nil {
		t.Fatalf("insert legacy open banking transactions: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("apply category backfill migration: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("repeat category backfill migration: %v", err)
	}

	type categoryRecord struct {
		category string
		metadata map[string]any
	}
	readRecord := func(externalID string) categoryRecord {
		t.Helper()
		var record categoryRecord
		var metadataJSON []byte
		if err := pool.QueryRow(ctx, `SELECT category,source_metadata FROM transactions
			WHERE user_id=$1 AND source_account_id=$2 AND external_id=$3`,
			user.ID, accountID, externalID,
		).Scan(&record.category, &metadataJSON); err != nil {
			t.Fatalf("read transaction %s: %v", externalID, err)
		}
		if err := json.Unmarshal(metadataJSON, &record.metadata); err != nil {
			t.Fatalf("decode transaction %s metadata: %v", externalID, err)
		}
		return record
	}

	mccFood := readRecord("mcc-food")
	if mccFood.category != "groceries" || mccFood.metadata["classification_source"] != "mcc" ||
		mccFood.metadata["classified_category"] != "groceries" {
		t.Fatalf("MCC backfill = %#v", mccFood)
	}
	keywordTransport := readRecord("keyword-transport")
	if keywordTransport.category != "transport" || keywordTransport.metadata["classification_source"] != "expense_keyword" {
		t.Fatalf("expense keyword backfill = %#v", keywordTransport)
	}
	incomeSalary := readRecord("income-salary")
	if incomeSalary.category != "salary" || incomeSalary.metadata["classification_source"] != "income_keyword" {
		t.Fatalf("income keyword backfill = %#v", incomeSalary)
	}
	legacyAutomaticCategories := map[string]string{
		"legacy-auto-salary":   "salary",
		"legacy-auto-refund":   "refund",
		"legacy-auto-food":     "groceries",
		"legacy-auto-shopping": "shopping",
	}
	for externalID, expectedCategory := range legacyAutomaticCategories {
		record := readRecord(externalID)
		if record.category != expectedCategory || record.metadata["classification_override"] != nil ||
			record.metadata["category_override"] != nil {
			t.Fatalf("legacy automatic classification %s was locked as an override: %#v", externalID, record)
		}
	}
	manualGift := readRecord("manual-gift")
	if manualGift.category != "gift" || manualGift.metadata["classification_override"] != true ||
		manualGift.metadata["category_override"] != true || manualGift.metadata["category_source"] != "user_override" {
		t.Fatalf("manual category preservation = %#v", manualGift)
	}
	overrideOther := readRecord("override-other")
	if overrideOther.category != "other" || overrideOther.metadata["category_source"] != "user_override" {
		t.Fatalf("explicit other override = %#v", overrideOther)
	}
	automaticFood := readRecord("automatic-food")
	if automaticFood.category != "groceries" || automaticFood.metadata["classified_category"] != "groceries" ||
		automaticFood.metadata["classification_override"] != nil {
		t.Fatalf("existing automatic classification = %#v", automaticFood)
	}
	if fallbackOther := readRecord("fallback-other"); fallbackOther.category != "other" {
		t.Fatalf("unmatched fallback = %#v", fallbackOther)
	}

	result, err := repo.ImportOpenBankingTransactions(ctx, user.ID, accountID, []OpenBankingTransactionSeed{
		{
			ExternalID: "legacy-auto-salary", Type: "income", Category: "freelance",
			Description: "ACME client invoice", Amount: "3200.00", Currency: "EUR",
			OccurredAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), Status: "booked",
			Metadata: []byte(`{
				"classification_source":"income_keyword",
				"classified_category":"freelance",
				"classified_type":"income",
				"category_source":"income_keyword"
			}`),
		},
		{
			ExternalID: "manual-gift", Type: "expense", Category: "transport",
			Description: "Updated BOLT ride", Amount: "21.00", Currency: "EUR",
			OccurredAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), Status: "booked",
			Metadata: []byte(`{
				"classification_source":"expense_keyword",
				"classified_category":"transport",
				"classified_type":"expense",
				"category_source":"expense_keyword"
			}`),
		},
	}, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	if err != nil || result.Updated != 2 {
		t.Fatalf("sync after migrated manual correction = %#v, %v", result, err)
	}
	legacySalary := readRecord("legacy-auto-salary")
	if legacySalary.category != "freelance" || legacySalary.metadata["category_source"] != "income_keyword" ||
		legacySalary.metadata["classification_override"] != nil {
		t.Fatalf("legacy automatic category after sync = %#v", legacySalary)
	}
	manualGift = readRecord("manual-gift")
	if manualGift.category != "gift" || manualGift.metadata["category_source"] != "user_override" ||
		manualGift.metadata["classified_category"] != "transport" {
		t.Fatalf("manual correction after sync = %#v", manualGift)
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
	if users != 1 || categories != 6 || transactions != 1 || quarantined != 8 || versions != 17 {
		t.Fatalf("legacy upgrade counts users=%d categories=%d transactions=%d quarantined=%d versions=%d", users, categories, transactions, quarantined, versions)
	}
	var email, transactionType, category, currency string
	if err := pool.QueryRow(ctx, "SELECT email FROM users").Scan(&email); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT type,category,currency FROM transactions").Scan(&transactionType, &category, &currency); err != nil {
		t.Fatal(err)
	}
	if email != "valid@example.com" || transactionType != "expense" || category != "groceries" || currency != "EUR" {
		t.Fatalf("legacy values were not normalized safely: email=%q type=%q category=%q currency=%q", email, transactionType, category, currency)
	}
	if _, err := repo.CreateTransaction(ctx, 1, model.TransactionRequest{
		Type: "expense", Category: "Food", Amount: "1.00", Currency: "USD", OccurredAt: "2026-07-11",
	}); err == nil {
		t.Fatal("hardened constraint accepted USD after legacy upgrade")
	}
}

func openIntegrationRepository(t *testing.T, requestedTimezone ...string) (context.Context, *Repository, *pgxpool.Pool) {
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
	if len(requestedTimezone) > 0 {
		query.Set("timezone", requestedTimezone[0])
	}
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

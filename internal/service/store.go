package service

import (
	"context"
	"time"

	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

var _ Store = (*repository.Repository)(nil)

// Store is the complete persistence contract used by Service. The embedded
// domain contracts keep each area navigable while preserving one constructor
// boundary for production and test stores.
type Store interface {
	lifecycleStore
	userStore
	categoryStore
	transactionStore
	transactionScheduleStore
	budgetStore
	notificationStore
	investmentStore
	openBankingStore
}

type lifecycleStore interface {
	Close()
	Ping(context.Context) error
}

type userStore interface {
	RegisterUser(context.Context, string, string) (model.User, error)
	FindUserByEmail(context.Context, string) (repository.UserWithPassword, error)
	GetUser(context.Context, int) (model.User, error)
	DeleteUser(context.Context, int) error
}

type categoryStore interface {
	EnsureDefaultCategories(context.Context, int) error
	ListCategories(context.Context, int, string) ([]model.Category, error)
	CreateCategory(context.Context, int, model.CategoryRequest) (model.Category, error)
	DeleteCategory(context.Context, int, int) error
	FindActiveCategoryName(context.Context, int, string, string) (string, error)
}

type transactionStore interface {
	ListTransactions(context.Context, int, repository.TransactionFilter) ([]model.Transaction, error)
	ExportTransactions(context.Context, int, time.Time, time.Time, int) ([]model.Transaction, error)
	CreateTransaction(context.Context, int, model.TransactionRequest) (model.Transaction, error)
	ImportTransactions(context.Context, int, []model.ImportedTransaction) (int, int, error)
	GetTransaction(context.Context, int, int) (model.Transaction, error)
	UpdateTransaction(context.Context, int, int, model.TransactionRequest) (model.Transaction, error)
	DeleteTransaction(context.Context, int, int) error
	Summary(context.Context, int, string, time.Time, time.Time) (model.Summary, error)
}

type transactionScheduleStore interface {
	CreateTransactionSchedule(context.Context, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error)
	ListTransactionSchedules(context.Context, int, string, time.Time) ([]model.TransactionSchedule, error)
	GetTransactionSchedule(context.Context, int, int, time.Time) (model.TransactionSchedule, error)
	UpdateTransactionSchedule(context.Context, int, int, model.TransactionScheduleRequest, time.Time) (model.TransactionSchedule, error)
	SetTransactionScheduleStatus(context.Context, int, int, string) error
	ArchiveTransactionSchedule(context.Context, int, int) error
	ListActiveTransactionSchedules(context.Context) ([]model.TransactionSchedule, error)
	UpsertTransactionScheduleOccurrences(context.Context, []repository.ScheduleOccurrenceSeed) (int, error)
	MarkTransactionScheduleMaterializedThrough(context.Context, int, time.Time) error
	ListTransactionScheduleOccurrences(context.Context, int, repository.ScheduleOccurrenceFilter) ([]model.TransactionScheduleOccurrence, error)
	PostDueTransactionScheduleOccurrences(context.Context, time.Time, int) (int, error)
	QueueDueTransactionScheduleReminders(context.Context, time.Time, int) (int, error)
}

type budgetStore interface {
	ListBudgets(context.Context, int, time.Time, bool) ([]model.Budget, error)
	GetBudget(context.Context, int, int, time.Time) (model.Budget, error)
	CreateBudget(context.Context, int, model.BudgetRequest, time.Time) (model.Budget, error)
	UpdateBudget(context.Context, int, int, model.BudgetRequest, time.Time) (model.Budget, error)
	ArchiveBudget(context.Context, int, int) error
	QueueBudgetAlerts(context.Context, time.Time) (int, error)
}

type notificationStore interface {
	GetNotificationPreferences(context.Context, int) (model.NotificationPreferences, error)
	UpdateNotificationPreferences(context.Context, int, model.NotificationPreferences) (model.NotificationPreferences, error)
	RegisterPushDevice(context.Context, int, model.PushDeviceRequest) (model.PushDevice, error)
	DeactivatePushDevice(context.Context, int, int) error
	ClaimNotificationDeliveries(context.Context, time.Time, time.Time, time.Time, []string, int) ([]repository.NotificationDelivery, error)
	CompleteNotificationDelivery(context.Context, int, bool, bool, bool, string, time.Time, time.Time) error
}

type investmentStore interface {
	CreateInvestmentTrade(context.Context, int, model.InvestmentTradeRequest) (model.InvestmentTrade, error)
	ListInvestmentTrades(context.Context, int, repository.InvestmentTradeFilter) ([]model.InvestmentTrade, error)
	DeleteInvestmentTrade(context.Context, int, int) error
	InvestmentHoldingQuantity(context.Context, int, string, string, string) (string, error)
	ListInvestmentPrices(context.Context) ([]model.InvestmentPrice, error)
	UpsertManualInvestmentPrice(context.Context, int, model.InvestmentPriceRequest, time.Time) (model.InvestmentPrice, error)
	CreateInvestmentSchedule(context.Context, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error)
	ListInvestmentSchedules(context.Context, int, string) ([]model.InvestmentSchedule, error)
	GetInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error)
	UpdateInvestmentSchedule(context.Context, int, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error)
	SetInvestmentScheduleStatus(context.Context, int, int, string) error
	ArchiveInvestmentSchedule(context.Context, int, int) error
	ListActiveInvestmentSchedules(context.Context) ([]model.InvestmentSchedule, error)
	QueueInvestmentReminder(context.Context, model.InvestmentSchedule, time.Time) (bool, error)
}

type openBankingStore interface {
	CreateOpenBankingAuthorization(context.Context, repository.NewOpenBankingAuthorization) (int, error)
	SetOpenBankingAuthorizationProviderID(context.Context, int, string) error
	ClaimOpenBankingAuthorization(context.Context, string, time.Time) (repository.OpenBankingAuthorizationRecord, error)
	FailOpenBankingAuthorization(context.Context, int, string, string) error
	StoreOpenBankingConnection(context.Context, repository.NewOpenBankingConnection) (int, error)
	ListOpenBankingConnections(context.Context, int) ([]model.OpenBankingConnection, error)
	GetOpenBankingConnection(context.Context, int, int) (repository.OpenBankingConnectionRecord, error)
	UpdateOpenBankingConnectionStatus(context.Context, int, int, string) error
	DeleteOpenBankingConnection(context.Context, int, int) error
	ListOpenBankingAccounts(context.Context, int) ([]model.OpenBankingAccount, error)
	GetOpenBankingAccount(context.Context, int, int) (repository.OpenBankingAccountRecord, error)
	ListOpenBankingProviderSessions(context.Context, int) ([]string, error)
	ImportOpenBankingTransactions(context.Context, int, int, []repository.OpenBankingTransactionSeed, time.Time) (model.OpenBankingSyncResult, error)
	ClaimOpenBankingAccountsForSync(context.Context, time.Time, time.Time, time.Time, int) ([]repository.OpenBankingSyncAccount, error)
	ReleaseOpenBankingSyncClaim(context.Context, int) error
}

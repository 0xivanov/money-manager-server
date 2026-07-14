package router

import (
	"context"

	"money-manager-server/internal/model"
)

type API interface {
	readinessAPI
	authenticationAPI
	profileAPI
	categoryAPI
	transactionAPI
	transactionScheduleAPI
	budgetAPI
	notificationAPI
	investmentAPI
	openBankingAPI
}

type readinessAPI interface {
	Ready(context.Context) error
}

type authenticationAPI interface {
	Register(context.Context, model.AuthRequest) (model.AuthResponse, error)
	Login(context.Context, model.AuthRequest) (model.AuthResponse, error)
	Authenticate(context.Context, string) (int, error)
}

type profileAPI interface {
	GetMe(context.Context, int) (model.User, error)
	DeleteMe(context.Context, int) error
}

type categoryAPI interface {
	ListCategories(context.Context, int, string) ([]model.Category, error)
	CreateCategory(context.Context, int, model.CategoryRequest) (model.Category, error)
	DeleteCategory(context.Context, int, int) error
}

type transactionAPI interface {
	ListTransactions(context.Context, int, string, string, string) ([]model.Transaction, error)
	ExportTransactions(context.Context, int, string, string) ([]model.Transaction, error)
	Summary(context.Context, int, string) (model.Summary, error)
	CreateTransaction(context.Context, int, model.TransactionRequest) (model.Transaction, error)
	UpdateTransaction(context.Context, int, int, model.TransactionRequest) (model.Transaction, error)
	DeleteTransaction(context.Context, int, int) error
	ImportRevolutCSV(context.Context, int, []byte) (model.ImportResult, error)
}

type transactionScheduleAPI interface {
	ListTransactionSchedules(context.Context, int, string) ([]model.TransactionSchedule, error)
	CreateTransactionSchedule(context.Context, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error)
	GetTransactionSchedule(context.Context, int, int) (model.TransactionSchedule, error)
	UpdateTransactionSchedule(context.Context, int, int, model.TransactionScheduleRequest) (model.TransactionSchedule, error)
	PauseTransactionSchedule(context.Context, int, int) (model.TransactionSchedule, error)
	ResumeTransactionSchedule(context.Context, int, int) (model.TransactionSchedule, error)
	DeleteTransactionSchedule(context.Context, int, int) error
	ListTransactionScheduleOccurrences(context.Context, int, string, string, int, string) ([]model.TransactionScheduleOccurrence, error)
}

type budgetAPI interface {
	ListBudgets(context.Context, int, bool) ([]model.Budget, error)
	GetBudget(context.Context, int, int) (model.Budget, error)
	CreateBudget(context.Context, int, model.BudgetRequest) (model.Budget, error)
	UpdateBudget(context.Context, int, int, model.BudgetRequest) (model.Budget, error)
	DeleteBudget(context.Context, int, int) error
}

type notificationAPI interface {
	GetNotificationPreferences(context.Context, int) (model.NotificationPreferences, error)
	UpdateNotificationPreferences(context.Context, int, model.NotificationPreferences) (model.NotificationPreferences, error)
	RegisterPushDevice(context.Context, int, model.PushDeviceRequest) (model.PushDevice, error)
	DeletePushDevice(context.Context, int, int) error
}

type investmentAPI interface {
	CreateInvestmentTrade(context.Context, int, model.InvestmentTradeRequest) (model.InvestmentTrade, error)
	ListInvestmentTrades(context.Context, int, string, string, string, string, string) ([]model.InvestmentTrade, error)
	DeleteInvestmentTrade(context.Context, int, int) error
	InvestmentPortfolio(context.Context, int) (model.InvestmentPortfolio, error)
	InvestmentPortfolioHistory(context.Context, int, string) (model.InvestmentPortfolioHistory, error)
	SetManualInvestmentPrice(context.Context, int, model.InvestmentPriceRequest) (model.InvestmentPrice, error)
	ExportInvestmentTrades(context.Context, int, string, string) ([]model.InvestmentTrade, error)
	ListInvestmentSchedules(context.Context, int, string) ([]model.InvestmentSchedule, error)
	CreateInvestmentSchedule(context.Context, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error)
	GetInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error)
	UpdateInvestmentSchedule(context.Context, int, int, model.InvestmentScheduleRequest) (model.InvestmentSchedule, error)
	PauseInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error)
	ResumeInvestmentSchedule(context.Context, int, int) (model.InvestmentSchedule, error)
	DeleteInvestmentSchedule(context.Context, int, int) error
}

type openBankingAPI interface {
	ListOpenBankingInstitutions(context.Context, string, string) ([]model.OpenBankingInstitution, error)
	StartOpenBankingAuthorization(context.Context, int, model.OpenBankingAuthorizationRequest) (model.OpenBankingAuthorization, error)
	CompleteOpenBankingAuthorization(context.Context, model.OpenBankingCallbackRequest) (model.OpenBankingCallbackResult, error)
	ListOpenBankingConnections(context.Context, int) ([]model.OpenBankingConnection, error)
	GetOpenBankingConnection(context.Context, int, int) (model.OpenBankingConnection, error)
	DeleteOpenBankingConnection(context.Context, int, int, model.OpenBankingPSUContext) error
	ListOpenBankingAccounts(context.Context, int) ([]model.OpenBankingAccount, error)
	GetOpenBankingAccountDetails(context.Context, int, int, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error)
	GetOpenBankingAccountBalances(context.Context, int, int, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error)
	GetOpenBankingAccountTransactions(context.Context, int, int, string, string, string, string, string, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error)
	SyncOpenBankingAccount(context.Context, int, int, string, string, model.OpenBankingPSUContext) (model.OpenBankingSyncResult, error)
}

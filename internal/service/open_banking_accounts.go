package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/enablebanking"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
)

func (s *Service) ListOpenBankingConnections(ctx context.Context, userID int) ([]model.OpenBankingConnection, error) {
	if _, err := s.requireOpenBanking(); err != nil {
		return nil, err
	}
	connections, err := s.store.ListOpenBankingConnections(ctx, userID)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list open banking connections: %w", err))
	}
	return connections, nil
}

func (s *Service) GetOpenBankingConnection(ctx context.Context, userID, connectionID int) (model.OpenBankingConnection, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return model.OpenBankingConnection{}, err
	}
	record, err := s.store.GetOpenBankingConnection(ctx, userID, connectionID)
	if err != nil {
		return model.OpenBankingConnection{}, mapOpenBankingRepositoryNotFound(err, "bank connection not found")
	}
	session, err := client.GetSession(ctx, record.ProviderSession)
	if err != nil {
		return model.OpenBankingConnection{}, mapOpenBankingProviderError("get session", err)
	}
	if session.Status != "" && session.Status != record.Connection.Status {
		if err := s.store.UpdateOpenBankingConnectionStatus(ctx, userID, connectionID, session.Status); err != nil {
			return model.OpenBankingConnection{}, apperrors.Internal(fmt.Errorf("update bank connection status: %w", err))
		}
		record.Connection.Status = session.Status
		record.Connection.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	}
	return record.Connection, nil
}

func (s *Service) DeleteOpenBankingConnection(ctx context.Context, userID, connectionID int, psu model.OpenBankingPSUContext) error {
	client, err := s.requireOpenBanking()
	if err != nil {
		return err
	}
	record, err := s.store.GetOpenBankingConnection(ctx, userID, connectionID)
	if err != nil {
		return mapOpenBankingRepositoryNotFound(err, "bank connection not found")
	}
	if err := client.DeleteSession(ctx, record.ProviderSession, providerPSUHeaders(psu)); err != nil && !providerSessionAlreadyGone(err) {
		return mapOpenBankingProviderError("delete session", err)
	}
	if err := s.store.DeleteOpenBankingConnection(ctx, userID, connectionID); err != nil {
		return mapOpenBankingRepositoryNotFound(err, "bank connection not found")
	}
	return nil
}

func (s *Service) ListOpenBankingAccounts(ctx context.Context, userID int) ([]model.OpenBankingAccount, error) {
	if _, err := s.requireOpenBanking(); err != nil {
		return nil, err
	}
	accounts, err := s.store.ListOpenBankingAccounts(ctx, userID)
	if err != nil {
		return nil, apperrors.Internal(fmt.Errorf("list open banking accounts: %w", err))
	}
	return accounts, nil
}

func (s *Service) GetOpenBankingAccountDetails(ctx context.Context, userID, accountID int, psu model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	if account.ProviderAccountID == "" {
		return account.ProviderPayload, nil
	}
	response, err := client.AccountDetails(ctx, account.ProviderAccountID, providerPSUHeaders(psu))
	if err != nil {
		return nil, mapOpenBankingProviderError("get account details", err)
	}
	return response, nil
}

func (s *Service) GetOpenBankingAccountBalances(ctx context.Context, userID, accountID int, psu model.OpenBankingPSUContext) (model.OpenBankingProviderData, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	if account.ProviderAccountID == "" {
		return nil, apperrors.NotFound("balances are not available for this account")
	}
	response, err := client.AccountBalances(ctx, account.ProviderAccountID, providerPSUHeaders(psu))
	if err != nil {
		return nil, mapOpenBankingProviderError("get account balances", err)
	}
	return response, nil
}

func (s *Service) GetOpenBankingAccountTransactions(
	ctx context.Context,
	userID int,
	accountID int,
	dateFrom string,
	dateTo string,
	continuationKey string,
	transactionStatus string,
	strategy string,
	psu model.OpenBankingPSUContext,
) (model.OpenBankingProviderData, error) {
	client, account, err := s.openBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	if account.ProviderAccountID == "" {
		return nil, apperrors.NotFound("transactions are not available for this account")
	}
	query, err := openBankingTransactionQuery(dateFrom, dateTo, continuationKey, transactionStatus, strategy, s.now().UTC())
	if err != nil {
		return nil, err
	}
	response, err := client.AccountTransactions(ctx, account.ProviderAccountID, query, providerPSUHeaders(psu))
	if err != nil {
		return nil, mapOpenBankingProviderError("get account transactions", err)
	}
	return response, nil
}

func (s *Service) openBankingAccount(ctx context.Context, userID, accountID int) (openBankingClient, repository.OpenBankingAccountRecord, error) {
	client, err := s.requireOpenBanking()
	if err != nil {
		return nil, repository.OpenBankingAccountRecord{}, err
	}
	account, err := s.store.GetOpenBankingAccount(ctx, userID, accountID)
	if err != nil {
		return nil, repository.OpenBankingAccountRecord{}, mapOpenBankingRepositoryNotFound(err, "bank account not found")
	}
	return client, account, nil
}

func providerPSUHeaders(psu model.OpenBankingPSUContext) enablebanking.PSUHeaders {
	return enablebanking.PSUHeaders{
		IPAddress: psu.IPAddress, UserAgent: psu.UserAgent, Referer: psu.Referer,
		Accept: psu.Accept, AcceptCharset: psu.AcceptCharset, AcceptEncoding: psu.AcceptEncoding,
		AcceptLanguage: psu.AcceptLanguage,
	}
}

func enableBankingEmptyPSUHeaders() enablebanking.PSUHeaders { return enablebanking.PSUHeaders{} }

func providerSessionAlreadyGone(err error) bool {
	var providerErr *enablebanking.ProviderError
	if !errors.As(err, &providerErr) {
		return false
	}
	if providerErr.StatusCode == http.StatusNotFound {
		return true
	}
	switch providerErr.Code {
	case "CLOSED_SESSION", "EXPIRED_SESSION", "SESSION_DOES_NOT_EXIST":
		return true
	default:
		return false
	}
}

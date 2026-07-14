package router

import (
	"bytes"
	"encoding/csv"
	"strconv"

	"money-manager-server/internal/model"
)

func transactionsCSV(transactions []model.Transaction) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{"occurred_at", "type", "category", "description", "amount", "currency", "source", "status", "excluded_from_budget"}); err != nil {
		return nil, err
	}
	for _, transaction := range transactions {
		if err := writer.Write([]string{
			transaction.OccurredAt,
			transaction.Type,
			transaction.Category,
			transaction.Description,
			transaction.Amount,
			transaction.Currency,
			transaction.Source,
			transaction.Status,
			strconv.FormatBool(transaction.ExcludedFromBudget),
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func investmentTradesCSV(trades []model.InvestmentTrade) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{
		"occurred_at", "asset_type", "symbol", "asset_name", "broker", "side",
		"amount", "quantity", "price_per_unit", "price_provider", "price_as_of",
		"fees", "currency", "notes",
	}); err != nil {
		return nil, err
	}
	for _, trade := range trades {
		if err := writer.Write([]string{
			trade.OccurredAt, trade.AssetType, trade.Symbol, trade.AssetName, trade.Broker,
			trade.Side, trade.Amount, trade.Quantity, trade.PricePerUnit, trade.PriceProvider,
			trade.PriceAsOf, trade.Fees, trade.Currency, trade.Notes,
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

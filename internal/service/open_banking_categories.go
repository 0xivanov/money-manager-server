package service

import (
	"strconv"
	"strings"
)

const (
	openBankingCategorySourceMCC            = "mcc"
	openBankingCategorySourceExpenseKeyword = "expense_keyword"
	openBankingCategorySourceIncomeKeyword  = "income_keyword"
	openBankingCategorySourceFallback       = "fallback"
)

type openBankingCategoryClassification struct {
	Category string
	Source   string
}

type openBankingCategoryKeywordRule struct {
	category string
	keywords []string
}

var openBankingExpenseKeywordRules = []openBankingCategoryKeywordRule{
	{category: "going_out", keywords: []string{
		"shisha", "hookah", "nightclub", "night club", "cocktail", "lounge",
		"club entry", "club ticket",
	}},
	{category: "groceries", keywords: []string{
		"lidl", "kaufland", "billa", "fantastico", "t-market", "supermarket", "grocery",
	}},
	{category: "dining_out", keywords: []string{
		"restaurant", "cafe", "coffee", "bakery", "takeaway", "glovo", "wolt", "deliveroo",
		"mcdonald", "kfc", "happy bar",
	}},
	{category: "transport", keywords: []string{
		"uber", "bolt", "taxi", "metro", "subway", "bus ticket", "tram", "railway", "train ticket",
		"parking", "fuel", "petrol", "gas station", "shell", "omv",
	}},
	{category: "housing", keywords: []string{
		"rent payment", "monthly rent", "landlord", "mortgage",
	}},
	{category: "utilities", keywords: []string{
		"electricity", "electric bill", "water bill", "heating", "utility", "internet bill", "broadband",
		"mobile bill", "phone bill", "telecom", "vivacom", "yettel",
	}},
	{category: "health", keywords: []string{
		"pharmacy", "apteka", "drugstore", "hospital", "clinic", "doctor", "dentist", "dental", "medical",
	}},
	{category: "entertainment", keywords: []string{
		"netflix", "spotify", "cinema", "movie theatre", "movie theater", "concert", "steam games",
		"playstation", "xbox",
	}},
	{category: "travel", keywords: []string{
		"airbnb", "booking.com", "hotel", "hostel", "airline", "flight", "ryanair", "wizz air", "easyjet",
	}},
	{category: "education", keywords: []string{
		"university", "school fee", "tuition", "online course", "udemy", "coursera",
	}},
	{category: "beauty", keywords: []string{
		"barber", "barbershop", "hair salon", "hairdresser", "haircut", "beauty salon",
		"nail salon", "nails", "manicure", "pedicure", "cosmetics", "makeup", "skin care",
		"skincare", "eyebrow", "brow studio", "lash studio", "waxing", "sephora", "douglas parfumerie",
	}},
	{category: "shopping", keywords: []string{
		"amazon", "ebay", "etsy", "shopping mall", "retail", "clothing", "fashion", "zara", "ikea",
		"dm drogerie",
	}},
}

var openBankingIncomeKeywordRules = []openBankingCategoryKeywordRule{
	{category: "salary", keywords: []string{"salary", "payroll", "monthly wage", "wages"}},
	{category: "freelance", keywords: []string{"freelance", "contractor payment", "client invoice"}},
	{category: "gift", keywords: []string{"gift"}},
	{category: "investment", keywords: []string{"dividend", "interest payment", "investment return"}},
	{category: "refund", keywords: []string{"refund", "reversal", "cashback", "chargeback", "reimbursement"}},
}

func classifyOpenBankingTransaction(transactionType, merchantCategoryCode, description string) openBankingCategoryClassification {
	if transactionType == "income" {
		if category := openBankingKeywordCategory(description, openBankingIncomeKeywordRules); category != "" {
			return openBankingCategoryClassification{Category: category, Source: openBankingCategorySourceIncomeKeyword}
		}
		return openBankingCategoryClassification{Category: "other", Source: openBankingCategorySourceFallback}
	}

	// Specific nightlife descriptions are more precise than generic restaurant MCCs.
	if category := openBankingKeywordCategory(description, openBankingExpenseKeywordRules[:1]); category != "" {
		return openBankingCategoryClassification{Category: category, Source: openBankingCategorySourceExpenseKeyword}
	}
	if category := openBankingMCCCategory(merchantCategoryCode); category != "" {
		return openBankingCategoryClassification{Category: category, Source: openBankingCategorySourceMCC}
	}
	if category := openBankingKeywordCategory(description, openBankingExpenseKeywordRules); category != "" {
		return openBankingCategoryClassification{Category: category, Source: openBankingCategorySourceExpenseKeyword}
	}
	return openBankingCategoryClassification{Category: "other", Source: openBankingCategorySourceFallback}
}

func openBankingKeywordCategory(description string, rules []openBankingCategoryKeywordRule) string {
	description = strings.ToLower(strings.Join(strings.Fields(description), " "))
	for _, rule := range rules {
		for _, keyword := range rule.keywords {
			if strings.Contains(description, keyword) {
				return rule.category
			}
		}
	}
	return ""
}

func openBankingMCCCategory(value string) string {
	code, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	switch {
	case code == 5411 || code == 5422 || code == 5441 || code == 5451 || code == 5462 || code == 5499:
		return "groceries"
	case code == 5813:
		return "going_out"
	case code == 5811 || code == 5812 || code == 5814:
		return "dining_out"
	case code == 4011 || code == 4111 || code == 4112 || code == 4121 || code == 4131 || code == 4789 ||
		code == 5541 || code == 5542 || code == 7523:
		return "transport"
	case code == 4900 || code == 4814 || code == 4899:
		return "utilities"
	case code == 6513:
		return "housing"
	case code == 5912 || code >= 8011 && code <= 8099:
		return "health"
	case code == 7832 || code == 7922 || code >= 7991 && code <= 7999:
		return "entertainment"
	case code >= 3000 && code <= 3999 || code == 4511 || code == 4722 || code == 7011:
		return "travel"
	case code == 8211 || code == 8220 || code == 8241 || code == 8244 || code == 8249 || code == 8299:
		return "education"
	case code >= 5000 && code <= 5999:
		return "shopping"
	default:
		return ""
	}
}

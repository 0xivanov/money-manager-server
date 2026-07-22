package repository

import (
	"encoding/json"
	"testing"
)

func TestOpenBankingOverrideMetadataKeepsFreshClassificationAndOverrideMarkers(t *testing.T) {
	metadata, err := openBankingOverrideMetadata(json.RawMessage(`{
		"classification_source":"expense_keyword",
		"classified_category":"transport",
		"classified_type":"expense",
		"category_source":"expense_keyword"
	}`), true, true)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(metadata, &values); err != nil {
		t.Fatal(err)
	}
	if values["classification_source"] != "expense_keyword" ||
		values["classified_category"] != "transport" ||
		values["classified_type"] != "expense" ||
		values["category_source"] != "user_override" ||
		values["classification_override"] != true ||
		values["type_override"] != true ||
		values["category_override"] != true {
		t.Fatalf("override metadata = %#v", values)
	}
}

func TestPreserveUserClarificationDuringOpenBankingRefresh(t *testing.T) {
	got := preserveUserClarification(
		"UPDATED BANK DESCRIPTION",
		"OLD BANK DESCRIPTION\nUser clarification: Shisha with friends",
	)
	want := "UPDATED BANK DESCRIPTION\nUser clarification: Shisha with friends"
	if got != want {
		t.Fatalf("preserveUserClarification() = %q, want %q", got, want)
	}
}

func TestPreserveUserClarificationLeavesOrdinaryDescriptionUnchanged(t *testing.T) {
	got := preserveUserClarification("UPDATED BANK DESCRIPTION", "Manual correction")
	if got != "UPDATED BANK DESCRIPTION" {
		t.Fatalf("preserveUserClarification() = %q", got)
	}
}

package service

import (
	"testing"
	"time"

	"money-manager-server/internal/apperrors"
)

func TestNormalizeScheduleRecurrenceDefaults(t *testing.T) {
	start := time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC)

	weekly, err := normalizeScheduleRecurrence(start, " WEEKLY ", 0, nil, nil)
	if err != nil {
		t.Fatalf("normalize weekly recurrence: %v", err)
	}
	if weekly.frequency != "weekly" || weekly.interval != 1 || weekly.dayOfWeek == nil || *weekly.dayOfWeek != 7 {
		t.Fatalf("unexpected weekly recurrence: %+v", weekly)
	}

	monthly, err := normalizeScheduleRecurrence(start, "monthly", 2, nil, nil)
	if err != nil {
		t.Fatalf("normalize monthly recurrence: %v", err)
	}
	if monthly.interval != 2 || monthly.dayOfMonth == nil || *monthly.dayOfMonth != 12 {
		t.Fatalf("unexpected monthly recurrence: %+v", monthly)
	}
}

func TestNormalizeScheduleRecurrenceRejectsInvalidRules(t *testing.T) {
	start := time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC)
	value := 1
	zero := 0
	thirtyTwo := 32

	tests := []struct {
		name       string
		frequency  string
		interval   int
		dayOfWeek  *int
		dayOfMonth *int
	}{
		{name: "frequency", frequency: "yearly", interval: 1},
		{name: "interval", frequency: "daily", interval: 366},
		{name: "daily weekday", frequency: "daily", interval: 1, dayOfWeek: &value},
		{name: "weekly month day", frequency: "weekly", interval: 1, dayOfMonth: &value},
		{name: "weekly weekday range", frequency: "weekly", interval: 1, dayOfWeek: &zero},
		{name: "monthly weekday", frequency: "monthly", interval: 1, dayOfWeek: &value},
		{name: "monthly month day range", frequency: "monthly", interval: 1, dayOfMonth: &thirtyTwo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeScheduleRecurrence(
				start,
				test.frequency,
				test.interval,
				test.dayOfWeek,
				test.dayOfMonth,
			)
			if apperrors.KindOf(err) != apperrors.KindValidation {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

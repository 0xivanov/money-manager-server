package recurrence

import (
	"reflect"
	"testing"
	"time"
)

func TestMonthlyOccurrencesClampToLastValidDay(t *testing.T) {
	rule := Rule{
		Frequency: "monthly", Interval: 1, StartDate: date("2026-01-31"), DayOfMonth: 31,
	}
	got, err := Occurrences(rule, date("2026-01-01"), date("2026-04-30"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}
	if !reflect.DeepEqual(format(got), want) {
		t.Fatalf("occurrences = %#v, want %#v", format(got), want)
	}
}

func TestMonthlyOccurrencesClampInLeapYear(t *testing.T) {
	rule := Rule{
		Frequency: "monthly", Interval: 1, StartDate: date("2028-01-31"), DayOfMonth: 31,
	}
	got, err := Occurrences(rule, date("2028-02-01"), date("2028-03-31"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2028-02-29", "2028-03-31"}
	if !reflect.DeepEqual(format(got), want) {
		t.Fatalf("occurrences = %#v, want %#v", format(got), want)
	}
}

func TestWeeklyOccurrencesUseISOWeekdayAndInterval(t *testing.T) {
	rule := Rule{
		Frequency: "weekly", Interval: 2, StartDate: date("2026-07-13"), DayOfWeek: 5,
	}
	got, err := Occurrences(rule, date("2026-07-13"), date("2026-08-31"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-07-17", "2026-07-31", "2026-08-14", "2026-08-28"}
	if !reflect.DeepEqual(format(got), want) {
		t.Fatalf("occurrences = %#v, want %#v", format(got), want)
	}
}

func TestDailyOccurrencesRespectRangeAndEndDate(t *testing.T) {
	end := date("2026-07-18")
	rule := Rule{
		Frequency: "daily", Interval: 2, StartDate: date("2026-07-13"), EndDate: &end,
	}
	got, err := Occurrences(rule, date("2026-07-14"), date("2026-07-30"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-07-15", "2026-07-17"}
	if !reflect.DeepEqual(format(got), want) {
		t.Fatalf("occurrences = %#v, want %#v", format(got), want)
	}
}

func date(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func format(values []time.Time) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = value.Format("2006-01-02")
	}
	return out
}

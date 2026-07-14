package recurrence

import (
	"errors"
	"time"
)

type Rule struct {
	Frequency  string
	Interval   int
	StartDate  time.Time
	EndDate    *time.Time
	DayOfWeek  int
	DayOfMonth int
}

func Occurrences(rule Rule, from, through time.Time) ([]time.Time, error) {
	if err := validate(rule); err != nil {
		return nil, err
	}
	from = dateOnly(from)
	through = dateOnly(through)
	if through.Before(from) {
		return nil, errors.New("recurrence: through must not be before from")
	}
	if from.Before(rule.StartDate) {
		from = rule.StartDate
	}
	if rule.EndDate != nil && through.After(*rule.EndDate) {
		through = *rule.EndDate
	}
	if through.Before(from) {
		return []time.Time{}, nil
	}

	next := nextOnOrAfter(rule, from)
	out := make([]time.Time, 0)
	for !next.After(through) {
		out = append(out, next)
		next = nextOnOrAfter(rule, next.AddDate(0, 0, 1))
	}
	return out, nil
}

func nextOnOrAfter(rule Rule, target time.Time) time.Time {
	target = dateOnly(target)
	switch rule.Frequency {
	case "daily":
		if !target.After(rule.StartDate) {
			return rule.StartDate
		}
		days := daysBetween(rule.StartDate, target)
		steps := ceilDiv(days, rule.Interval)
		return rule.StartDate.AddDate(0, 0, steps*rule.Interval)
	case "weekly":
		first := rule.StartDate.AddDate(0, 0, (rule.DayOfWeek-isoWeekday(rule.StartDate)+7)%7)
		if !target.After(first) {
			return first
		}
		days := daysBetween(first, target)
		steps := ceilDiv(days, 7*rule.Interval)
		return first.AddDate(0, 0, steps*7*rule.Interval)
	case "monthly":
		year, month, _ := rule.StartDate.Date()
		first := clampedDate(year, month, rule.DayOfMonth)
		if first.Before(rule.StartDate) {
			first = addClampedMonths(first, rule.Interval, rule.DayOfMonth)
		}
		if !target.After(first) {
			return first
		}
		months := monthsBetween(first, target)
		steps := months / rule.Interval
		candidate := addClampedMonths(first, steps*rule.Interval, rule.DayOfMonth)
		if candidate.Before(target) {
			candidate = addClampedMonths(candidate, rule.Interval, rule.DayOfMonth)
		}
		return candidate
	default:
		panic("validated recurrence frequency became invalid")
	}
}

func validate(rule Rule) error {
	rule.StartDate = dateOnly(rule.StartDate)
	if rule.StartDate.IsZero() {
		return errors.New("recurrence: start date is required")
	}
	if rule.Interval < 1 {
		return errors.New("recurrence: interval must be positive")
	}
	if rule.EndDate != nil {
		end := dateOnly(*rule.EndDate)
		if end.Before(rule.StartDate) {
			return errors.New("recurrence: end date must not be before start date")
		}
	}
	switch rule.Frequency {
	case "daily":
		return nil
	case "weekly":
		if rule.DayOfWeek < 1 || rule.DayOfWeek > 7 {
			return errors.New("recurrence: weekly day must be between 1 and 7")
		}
		return nil
	case "monthly":
		if rule.DayOfMonth < 1 || rule.DayOfMonth > 31 {
			return errors.New("recurrence: monthly day must be between 1 and 31")
		}
		return nil
	default:
		return errors.New("recurrence: unsupported frequency")
	}
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func isoWeekday(value time.Time) int {
	weekday := int(value.Weekday())
	if weekday == 0 {
		return 7
	}
	return weekday
}

func daysBetween(from, to time.Time) int {
	return int(dateOnly(to).Sub(dateOnly(from)).Hours() / 24)
}

func monthsBetween(from, to time.Time) int {
	fromYear, fromMonth, _ := from.Date()
	toYear, toMonth, _ := to.Date()
	return (toYear-fromYear)*12 + int(toMonth-fromMonth)
}

func addClampedMonths(value time.Time, months, day int) time.Time {
	year, month, _ := value.Date()
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, months, 0)
	return clampedDate(monthStart.Year(), monthStart.Month(), day)
}

func clampedDate(year int, month time.Month, day int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func ceilDiv(value, divisor int) int {
	return (value + divisor - 1) / divisor
}

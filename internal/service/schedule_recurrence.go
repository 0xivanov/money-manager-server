package service

import (
	"strings"
	"time"

	"money-manager-server/internal/apperrors"
)

type normalizedScheduleRecurrence struct {
	frequency  string
	interval   int
	dayOfWeek  *int
	dayOfMonth *int
}

func normalizeScheduleRecurrence(
	start time.Time,
	frequency string,
	interval int,
	dayOfWeek, dayOfMonth *int,
) (normalizedScheduleRecurrence, error) {
	frequency = strings.ToLower(strings.TrimSpace(frequency))
	if frequency != "daily" && frequency != "weekly" && frequency != "monthly" {
		return normalizedScheduleRecurrence{}, apperrors.Validation("frequency must be daily, weekly, or monthly")
	}
	if interval == 0 {
		interval = 1
	}
	if interval < 1 || interval > maximumScheduleInterval {
		return normalizedScheduleRecurrence{}, apperrors.Validation("frequency_interval must be between 1 and 365")
	}

	switch frequency {
	case "daily":
		if dayOfWeek != nil || dayOfMonth != nil {
			return normalizedScheduleRecurrence{}, apperrors.Validation("daily schedules cannot set day_of_week or day_of_month")
		}
	case "weekly":
		if dayOfMonth != nil {
			return normalizedScheduleRecurrence{}, apperrors.Validation("weekly schedules cannot set day_of_month")
		}
		if dayOfWeek == nil {
			value := isoWeekday(start)
			dayOfWeek = &value
		}
		if *dayOfWeek < 1 || *dayOfWeek > 7 {
			return normalizedScheduleRecurrence{}, apperrors.Validation("day_of_week must be between 1 and 7")
		}
	case "monthly":
		if dayOfWeek != nil {
			return normalizedScheduleRecurrence{}, apperrors.Validation("monthly schedules cannot set day_of_week")
		}
		if dayOfMonth == nil {
			value := start.Day()
			dayOfMonth = &value
		}
		if *dayOfMonth < 1 || *dayOfMonth > 31 {
			return normalizedScheduleRecurrence{}, apperrors.Validation("day_of_month must be between 1 and 31")
		}
	}

	return normalizedScheduleRecurrence{
		frequency:  frequency,
		interval:   interval,
		dayOfWeek:  dayOfWeek,
		dayOfMonth: dayOfMonth,
	}, nil
}

func scheduleLocalDate(now time.Time, timezone string) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC), nil
}

func isoWeekday(value time.Time) int {
	weekday := int(value.Weekday())
	if weekday == 0 {
		return 7
	}
	return weekday
}

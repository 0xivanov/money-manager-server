package service

import (
	"time"

	"money-manager-server/internal/model"
	"money-manager-server/internal/recurrence"
)

func investmentRecurrenceRule(schedule model.InvestmentSchedule) (recurrence.Rule, error) {
	start, err := time.Parse("2006-01-02", schedule.StartDate)
	if err != nil {
		return recurrence.Rule{}, err
	}
	var end *time.Time
	if schedule.EndDate != "" {
		value, err := time.Parse("2006-01-02", schedule.EndDate)
		if err != nil {
			return recurrence.Rule{}, err
		}
		end = &value
	}
	rule := recurrence.Rule{
		Frequency: schedule.Frequency, Interval: schedule.FrequencyInterval,
		StartDate: start, EndDate: end,
	}
	if schedule.DayOfWeek != nil {
		rule.DayOfWeek = *schedule.DayOfWeek
	}
	if schedule.DayOfMonth != nil {
		rule.DayOfMonth = *schedule.DayOfMonth
	}
	return rule, nil
}

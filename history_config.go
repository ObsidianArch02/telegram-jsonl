package main

import (
	"errors"
	"fmt"
	"time"
)

type historyConfig struct {
	Since    time.Time
	Disabled bool
}

func parseHistoryConfig(days int, since string, daysExplicit bool, now time.Time) (historyConfig, error) {
	if days < -1 || days > 36500 {
		return historyConfig{}, errors.New("history-days must be -1, 0, or 1-36500")
	}
	if since != "" {
		if daysExplicit {
			return historyConfig{}, errors.New("choose either --history-days or --history-since")
		}
		date, err := time.Parse(time.RFC3339, since)
		if err != nil {
			date, err = time.Parse("2006-01-02", since)
		}
		if err != nil || date.Unix() < 0 || date.After(now) {
			return historyConfig{}, errors.New("history-since must be a past RFC3339 timestamp or YYYY-MM-DD date (UTC)")
		}
		return historyConfig{Since: date.UTC()}, nil
	}
	switch days {
	case -1:
		return historyConfig{}, nil
	case 0:
		return historyConfig{Disabled: true}, nil
	default:
		return historyConfig{Since: now.UTC().Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Second)}, nil
	}
}

func rollingHistoryConfig(days int, since time.Time, now time.Time) historyConfig {
	if !since.IsZero() {
		return historyConfig{Since: since}
	}
	switch days {
	case -1:
		return historyConfig{}
	case 0:
		return historyConfig{Disabled: true}
	default:
		return historyConfig{Since: now.UTC().Add(-time.Duration(days) * 24 * time.Hour).Truncate(time.Second)}
	}
}

func (h historyConfig) unix() int64 {
	if h.Since.IsZero() {
		return 0
	}
	return h.Since.Unix()
}

func (h historyConfig) args() []string {
	if h.Disabled {
		return []string{"--history-days", "0"}
	}
	if h.Since.IsZero() {
		return []string{"--history-days", "-1"}
	}
	return []string{"--history-since", h.Since.Format(time.RFC3339)}
}

func (h historyConfig) String() string {
	if h.Disabled {
		return "disabled (live updates and existing archive reconciliation remain enabled)"
	}
	if h.Since.IsZero() {
		return "all accessible history"
	}
	return fmt.Sprintf("messages dated on or after %s", h.Since.In(time.Local).Format(time.RFC3339))
}

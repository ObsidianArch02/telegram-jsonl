// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

const defaultRetentionBufferDays = 5

type retentionPolicy struct {
	Days int
}

func parseRetentionPolicy(value string, historyDays int) (retentionPolicy, error) {
	if value == "auto" {
		if historyDays < 0 {
			return retentionPolicy{Days: -1}, nil
		}
		return retentionPolicy{Days: historyDays + defaultRetentionBufferDays}, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < -1 || days == 0 || days > 36505 {
		return retentionPolicy{}, errors.New("retention-days must be auto, -1, or 1-36505")
	}
	if days == -1 {
		return retentionPolicy{Days: -1}, nil
	}
	if historyDays < 0 {
		return retentionPolicy{}, errors.New("retention-days must be -1 when history-days is -1")
	}
	if days <= historyDays {
		return retentionPolicy{}, errors.New("retention-days must be greater than history-days")
	}
	return retentionPolicy{Days: days}, nil
}

func (p retentionPolicy) cutoff(now time.Time) time.Time {
	if p.Days < 0 {
		return time.Time{}
	}
	return now.UTC().Add(-time.Duration(p.Days) * 24 * time.Hour).Truncate(time.Second)
}

func (p retentionPolicy) String() string {
	if p.Days < 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d days", p.Days)
}

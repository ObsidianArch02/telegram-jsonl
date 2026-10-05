// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"strings"
	"testing"
	"time"
)

func TestRollingHistoryConfigMovesWithEachCycle(t *testing.T) {
	first := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	one := rollingHistoryConfig(7, time.Time{}, first)
	two := rollingHistoryConfig(7, time.Time{}, second)
	if !two.Since.After(one.Since) || !two.Since.Equal(second.Add(-7*24*time.Hour)) {
		t.Fatalf("history boundary did not move with the cycle: first=%s second=%s", one.Since, two.Since)
	}
	if got := rollingHistoryConfig(0, time.Time{}, first); !got.Disabled {
		t.Fatal("zero history days did not disable backfill")
	}
	fixed := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if got := rollingHistoryConfig(7, fixed, second); !got.Since.Equal(fixed) {
		t.Fatal("explicit history-since was not kept fixed")
	}
}

func TestParseRetentionPolicyUsesHistoryBuffer(t *testing.T) {
	policy, err := parseRetentionPolicy("auto", 2)
	requireOK(t, err)
	if policy.Days != 7 {
		t.Fatalf("default retention buffer is wrong: %d", policy.Days)
	}
	policy, err = parseRetentionPolicy("10", 2)
	requireOK(t, err)
	if policy.Days != 10 {
		t.Fatalf("explicit retention was not accepted: %d", policy.Days)
	}
	if _, err := parseRetentionPolicy("2", 2); err == nil {
		t.Fatal("retention equal to history window was accepted")
	}
	policy, err = parseRetentionPolicy("auto", -1)
	requireOK(t, err)
	if policy.Days != -1 {
		t.Fatal("unlimited history did not select unlimited retention")
	}
}

func TestArchivePruneRemovesExpiredJSONLContent(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	old := row("user-7", 1, "old")
	old.Date = now.Add(-48 * time.Hour)
	fresh := row("user-7", 2, "fresh")
	fresh.Date = now.Add(-2 * time.Hour)
	requireOK(t, a.upsert([]Record{old, fresh}, true, 0))
	removed, _, after, err := a.prune(now.Add(-24*time.Hour), 0)
	requireOK(t, err)
	if removed != 1 || after <= 0 || strings.Contains(fileText(t, a, "user-7"), "\"message_id\":1") {
		t.Fatalf("expired JSONL content was not removed: removed=%d after=%d file=%q", removed, after, fileText(t, a, "user-7"))
	}
	if ids := a.ids("user-7"); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("unexpected records after retention cleanup: %v", ids)
	}
}

func TestArchivePruneEnforcesJSONLByteLimitFromOldest(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	rows := []Record{}
	for id := 1; id <= 3; id++ {
		r := row("user-7", id, "same-size")
		r.Date = now.Add(time.Duration(id-3) * time.Hour)
		rows = append(rows, r)
	}
	requireOK(t, a.upsert(rows, true, 0))
	limit, err := encodedRecordSize(rows[2])
	requireOK(t, err)
	removed, _, after, err := a.prune(time.Time{}, limit)
	requireOK(t, err)
	if removed != 2 || after > limit {
		t.Fatalf("byte limit did not remove oldest records: removed=%d after=%d limit=%d", removed, after, limit)
	}
	if ids := a.ids("user-7"); len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("byte limit kept the wrong records: %v", ids)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := appLogger
	var output bytes.Buffer
	appLogger = newAppLogger(&output)
	t.Cleanup(func() { appLogger = previous })
	return &output
}

func TestArchiveLogsPersistedChangesWithoutMessageText(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	output := captureLogs(t)
	original := row("user-7", 1, "private-original-body")
	requireOK(t, a.upsert([]Record{original, original}, true, 0))
	if got := output.String(); !strings.Contains(got, "added=1 updated=0 total=1 live=true") || strings.Count(got, "JSONL saved:") != 1 {
		t.Fatal("persisted additions were not reported accurately")
	}
	output.Reset()
	requireOK(t, a.upsert([]Record{original}, true, 0))
	if output.Len() != 0 {
		t.Fatal("unchanged record reported as a new write")
	}
	requireOK(t, a.upsert([]Record{row("user-7", 1, "private-edited-body")}, true, 0))
	requireOK(t, a.remove("user-7", []int{1, 1, 999}, true, 0))
	if got := output.String(); !strings.Contains(got, "added=0 updated=1 total=1") || !strings.Contains(got, "removed=1 total=0") || strings.Contains(got, "private-") {
		t.Fatal("update/deletion logs were inaccurate or exposed message text")
	}
	output.Reset()
	requireOK(t, a.upsert([]Record{original}, true, 0))
	if output.Len() != 0 {
		t.Fatal("excluded record reported as a new write")
	}
	peers, records := a.counts()
	if peers != 1 || records != 0 {
		t.Fatal("archive status counts disagree with persisted changes")
	}
}

func TestArchiveDoesNotLogSuccessfulWriteAfterFailure(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	output := captureLogs(t)
	a.dir = filepath.Join(t.TempDir(), "missing", "archive")
	if err := a.upsert([]Record{row("user-7", 1, "private-body")}, true, 0); err == nil || output.Len() != 0 {
		t.Fatal("failed persistence reported as successful")
	}
}

func TestHistoryLogUsesLocalTimezoneWithoutChangingBoundary(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("test-local", 8*60*60)
	t.Cleanup(func() { time.Local = previous })
	h := historyConfig{Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	if !strings.Contains(h.String(), "2026-09-01T08:00:00+08:00") {
		t.Fatal("human-readable history boundary ignored local timezone")
	}
	if args := h.args(); len(args) != 2 || args[1] != "2026-09-01T00:00:00Z" || h.Since.Location() != time.UTC {
		t.Fatal("display timezone changed the stored or command-line boundary")
	}
}

func TestFetchLogsReferenceRetryAndVerifiedOutcomeWithoutContent(t *testing.T) {
	output := captureLogs(t)
	m := attachmentMessage(8)
	f, _ := fakeFetcher(t, func(int) *tg.Message { return m }, []byte("pdf-data"), true)
	result := f.download(context.Background(), row("user-7", 1, "private-caption"), PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
	if result.Status != "downloaded" {
		t.Fatal("synthetic attachment download failed")
	}
	got := output.String()
	for _, expected := range []string{"reference expired", "attempt=2", "rechecking availability", "status=downloaded bytes=8"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing download stage: %s", expected)
		}
	}
	for _, private := range []string{"private-caption", "fresh-reference", "invoice", "pdf-data"} {
		if strings.Contains(got, private) {
			t.Fatal("attachment logs exposed private content or authorization metadata")
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

const syncCycleKey = "sync_cycle"

type syncCycle struct {
	StartedAt       time.Time     `json:"started_at"`
	HistorySince    int64         `json:"history_since"`
	HistoryDisabled bool          `json:"history_disabled"`
	ReconcileWindow time.Duration `json:"reconcile_window"`
	Peers           []string      `json:"peers"`
	Phase           string        `json:"phase"`
	Position        int           `json:"position"`
}

type reconciliationJob struct {
	IDs      []int `json:"ids"`
	Position int   `json:"position"`
	Since    int64 `json:"since"`
}

type historyJob struct {
	Offset int   `json:"offset"`
	MinID  int   `json:"min_id"`
	Newest int   `json:"newest"`
	Since  int64 `json:"since"`
	Pages  int   `json:"pages"`
	Done   bool  `json:"done"`
}

func parseReconcileWindow(value string) (time.Duration, error) {
	if value == "all" {
		return -1, nil
	}
	window, err := time.ParseDuration(value)
	if err != nil || window < 0 {
		return 0, errors.New("reconcile-window must be a nonnegative duration or all")
	}
	return window, nil
}

func syncJobKey(mode, peer string) string { return "sync_job:" + mode + ":" + peer }

func (a *archive) saveJob(key string, value any) error {
	return a.failure.report(a.state.WriteJSON(key, value))
}

func (a *archive) reconciliationIDs(peer string, since int64) []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	var ids []int
	for id, record := range a.rows[peer] {
		if since == 0 || record.Date.Unix() >= since {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func (r *receiver) openReconciliationJob(peer string) (reconciliationJob, error) {
	key := syncJobKey("reconcile", peer)
	var job reconciliationJob
	err := r.archive.state.ReadJSON(key, &job)
	if err == nil {
		if job.Position < 0 || job.Position > len(job.IDs) {
			return job, errors.New("invalid persisted reconciliation position")
		}
		for i, id := range job.IDs {
			if id <= 0 || i > 0 && id <= job.IDs[i-1] {
				return job, errors.New("invalid persisted reconciliation IDs")
			}
		}
		return job, nil
	}
	if !os.IsNotExist(err) {
		return job, err
	}
	if r.reconcileWindow > 0 {
		started := r.cycleStarted
		if started.IsZero() {
			started = time.Now()
		}
		job.Since = started.Add(-r.reconcileWindow).Unix()
	}
	job.IDs = r.archive.reconciliationIDs(peer, job.Since)
	return job, r.archive.saveJob(key, job)
}

func (r *receiver) openHistoryJob(peer string, p PeerInfo, incremental bool) (historyJob, error) {
	mode := "backfill"
	if incremental {
		mode = "catchup"
	}
	key := syncJobKey(mode, peer)
	var job historyJob
	err := r.archive.state.ReadJSON(key, &job)
	if err == nil {
		if job.Offset < 0 || job.MinID < 0 || job.Newest < 0 || job.Pages < 0 || job.Since < 0 {
			return job, errors.New("invalid persisted history job")
		}
		if r.historyPolicy.unix() >= job.Since {
			return job, nil
		}
		// An explicitly expanded window needs a new scan; ordinary restarts resume.
	} else if !os.IsNotExist(err) {
		return job, err
	}
	job = historyJob{Offset: p.Offset, Newest: p.Newest, Since: r.historyPolicy.unix()}
	if incremental {
		job.Offset, job.MinID = 0, p.Newest
	}
	return job, r.archive.saveJob(key, job)
}

func (a *archive) commitHistoryJob(peer string, job historyJob, incremental bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.meta.Peers[peer]
	if !ok {
		return errors.New("history peer disappeared")
	}
	mode := "backfill"
	if incremental {
		mode = "catchup"
		if job.Done {
			p.Newest = max(p.Newest, job.Newest)
		}
	} else {
		p.Offset, p.HistoryDone = job.Offset, job.Done
		p.Newest = max(p.Newest, job.Newest)
	}
	a.meta.Peers[peer] = p
	return a.failure.report(a.state.WriteJSONBatch(map[string]any{
		"archive_metadata":     a.hotMeta(),
		syncJobKey(mode, peer): job,
	}, nil))
}

func (a *archive) requestRepair(peer string) error {
	if peer != "" && !validPeer.MatchString(peer) {
		return errors.New("invalid repair peer")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	requests := map[string]bool{}
	if err := a.state.ReadJSON("repair_requests", &requests); err != nil && !os.IsNotExist(err) {
		return a.failure.report(err)
	}
	requests[peer] = true
	return a.saveJob("repair_requests", requests)
}

func (a *archive) finishCycle(cycle syncCycle) error {
	keys := []string{syncCycleKey, "repair_requests"}
	for _, peer := range cycle.Peers {
		for _, mode := range []string{"reconcile", "catchup", "backfill"} {
			keys = append(keys, syncJobKey(mode, peer))
		}
	}
	return a.failure.report(a.state.WriteJSONBatch(nil, keys))
}

func (r *receiver) loadCycle() (syncCycle, bool, error) {
	var cycle syncCycle
	err := r.archive.state.ReadJSON(syncCycleKey, &cycle)
	if os.IsNotExist(err) {
		return cycle, false, nil
	}
	if err != nil {
		return cycle, false, err
	}
	if cycle.Phase != "reconcile" && cycle.Phase != "history" || cycle.Position < 0 || cycle.Position > len(cycle.Peers) || cycle.StartedAt.IsZero() || cycle.HistorySince < 0 {
		return cycle, false, errors.New("invalid persisted synchronization cycle")
	}
	for _, peer := range cycle.Peers {
		if !validPeer.MatchString(peer) {
			return cycle, false, fmt.Errorf("invalid persisted synchronization peer")
		}
	}
	return cycle, true, nil
}

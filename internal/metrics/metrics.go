// Package metrics keeps small in-process counters and gauges for the /metrics
// endpoint. Values reset on restart — that is correct Prometheus counter
// semantics (rate()/increase() handle counter resets); deriving "totals" from
// live table rows is not, because retention pruning makes row counts shrink.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// DurationBuckets are the upper bounds (seconds) of the run-duration histogram.
var DurationBuckets = []float64{1, 5, 15, 60, 300, 900, 3600, 14400}

// maxDagLabels caps per-DAG label cardinality; beyond it new DAGs aggregate
// into the "_other" label so a pathological instance can't blow up scrapes.
const maxDagLabels = 200

// dagHist is one cumulative histogram (counts per bucket + sum/count).
type dagHist struct {
	counts [9]uint64 // len(DurationBuckets)+1; last is +Inf
	sumSec float64
	n      uint64
}

var (
	mu           sync.Mutex
	runsFinished = map[string]uint64{}   // state -> count since process start
	durByDag     = map[string]*dagHist{} // dag_id -> histogram since process start
	lastTickUnix atomic.Int64            // unix seconds of the last completed scheduler tick
	notifyFails  atomic.Uint64           // webhook notification delivery failures
)

// IncRunFinished records a run reaching a terminal state (success, failed,
// timed_out, cancelled). Call once per live→terminal transition only — never
// for recorded-outcome overrides (mark) of an already-terminal run.
func IncRunFinished(state string) {
	mu.Lock()
	runsFinished[state]++
	mu.Unlock()
}

// ObserveRunDuration records a finished run's wall-clock duration under its DAG.
func ObserveRunDuration(dagID string, d time.Duration) {
	if d < 0 {
		return
	}
	sec := d.Seconds()
	mu.Lock()
	h := durByDag[dagID]
	if h == nil {
		if len(durByDag) >= maxDagLabels {
			dagID = "_other"
			h = durByDag[dagID]
		}
		if h == nil {
			h = &dagHist{}
			durByDag[dagID] = h
		}
	}
	idx := len(DurationBuckets)
	for i, ub := range DurationBuckets {
		if sec <= ub {
			idx = i
			break
		}
	}
	h.counts[idx]++
	h.sumSec += sec
	h.n++
	mu.Unlock()
}

// SetLastTick stamps the completion time of a scheduler tick; /readyz and
// /metrics use it to detect a stalled scheduling loop.
func SetLastTick(t time.Time) { lastTickUnix.Store(t.Unix()) }

// LastTick returns the unix seconds of the last completed tick (0 = never).
func LastTick() int64 { return lastTickUnix.Load() }

// IncNotifyFailure counts a webhook notification that failed to deliver.
func IncNotifyFailure() { notifyFails.Add(1) }

// NotifyFailures returns the delivery-failure count since process start.
func NotifyFailures() uint64 { return notifyFails.Load() }

// RunsFinishedSnapshot returns a copy of the finished-run counters.
func RunsFinishedSnapshot() map[string]uint64 {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]uint64, len(runsFinished))
	for k, v := range runsFinished {
		out[k] = v
	}
	return out
}

// DurationSnapshot returns per-DAG histograms as (dagID, cumulative bucket
// counts aligned to DurationBuckets + a final +Inf bucket, sum seconds, count),
// sorted by dagID for stable scrape output.
type DurationSeries struct {
	DagID      string
	Cumulative []uint64 // len(DurationBuckets)+1, cumulative (le semantics)
	SumSec     float64
	Count      uint64
}

func DurationSnapshot() []DurationSeries {
	mu.Lock()
	defer mu.Unlock()
	out := make([]DurationSeries, 0, len(durByDag))
	for id, h := range durByDag {
		cum := make([]uint64, len(h.counts))
		var acc uint64
		for i, c := range h.counts {
			acc += c
			cum[i] = acc
		}
		out = append(out, DurationSeries{DagID: id, Cumulative: cum, SumSec: h.sumSec, Count: h.n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DagID < out[j].DagID })
	return out
}

// Reset clears all counters — test helper only.
func Reset() {
	mu.Lock()
	runsFinished = map[string]uint64{}
	durByDag = map[string]*dagHist{}
	mu.Unlock()
	lastTickUnix.Store(0)
	notifyFails.Store(0)
}

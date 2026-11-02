package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsClearProspectiveAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter statistics.StatementFilter
		before bool
		fenced bool
	}{
		{"result_before_begin", statistics.StatementFilter{UserID: 20}, true, false},
		{"original_before_begin", statistics.StatementFilter{UserID: 10}, true, false},
		{"full_before_begin", statistics.StatementFilter{}, true, false},
		{"result_after_begin", statistics.StatementFilter{UserID: 20}, false, true},
		{"original_after_begin", statistics.StatementFilter{UserID: 10}, false, true},
		{"full_after_begin", statistics.StatementFilter{}, false, true},
		{"unrelated_after_begin", statistics.StatementFilter{UserID: 99}, false, false},
	} {
		for _, evict := range []bool{false, true} {
			name := tc.name + "/retained"
			if evict {
				name = tc.name + "/evicted"
			}
			t.Run(name, func(t *testing.T) {
				now := time.Unix(100, 0)
				store := statistics.NewStatements(1, now)
				original := statementKey(1, 1)
				terminal := original
				terminal.UserID = 20
				if tc.before {
					store.Clear(tc.filter, false, now.Add(time.Second))
				}
				// Both keys register before any simulated identity mutation. The
				// resulting-key clear must fence even without a resulting entry.
				old := store.Begin(original, terminal, "SET ROLE $1", now.Add(2*time.Second))
				defer store.Release(old)
				if evict {
					pressure := store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now)
					store.RecordExecution(pressure, statistics.StatementRecord{Key: statementKey(2, 2), Query: "pressure", At: now, Rows: 1, Milliseconds: 1})
					store.Release(pressure)
				}
				if !tc.before {
					store.Clear(tc.filter, false, now.Add(3*time.Second))
				}
				fresh := store.Begin(terminal, terminal, "current representative", now.Add(4*time.Second))
				store.RecordExecution(fresh, statistics.StatementRecord{Key: terminal, Query: "fresh raw", At: now.Add(4 * time.Second), Rows: 2, Milliseconds: 2})
				store.Release(fresh)
				store.RecordExecution(old, statistics.StatementRecord{Key: terminal, Query: "SET ROLE target", At: now.Add(5 * time.Second), Rows: 3, Milliseconds: 6})
				entry := statementEntryFor(t, store, terminal)
				calls, rows, total := int64(2), int64(5), float64(8)
				if tc.fenced {
					calls, rows, total = 1, 2, 2
				}
				if entry.Execution.Count != calls || entry.Rows != rows || entry.Execution.Total != total || entry.Query != "current representative" || entry.StatsSince != now.Add(4*time.Second) || len(store.Snapshot()) != 1 {
					t.Fatal("prospective clear registration or bounded same-key merge", tc, entry, store.Snapshot())
				}
			})
		}
	}
}

func TestPgStatStatementsClearUnanticipatedTerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		user   uint32
		fenced bool
	}{
		{"actual_before_registration", 30, false},
		{"candidate_only", 20, false},
		{"original", 10, true},
		{"unrelated", 99, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			original := statementKey(1, 1)
			candidate, actual := original, original
			candidate.UserID, actual.UserID = 20, 30
			token := store.Begin(original, candidate, "CALL dynamic_role()", now)
			defer store.Release(token)
			store.Release(store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now))
			store.Clear(statistics.StatementFilter{UserID: tc.user}, false, now.Add(time.Second))
			at := now.Add(2 * time.Second)
			record := statistics.StatementRecord{Key: actual, Query: "CALL dynamic_role()", At: at, Rows: 3, Milliseconds: 6}
			store.RecordExecution(token, record)
			if tc.fenced {
				if len(store.Snapshot()) != 0 {
					t.Fatal("original admission clear lost after unknown terminal registration", store.Snapshot())
				}
				return
			}
			entry := statementEntryFor(t, store, actual)
			if entry.Execution.Count != 1 || entry.Rows != 3 || entry.Execution.Total != 6 || entry.Query != record.Query || entry.StatsSince != at || entry.MinMaxStatsSince != at {
				t.Fatal("successful unknown terminal was lost or retained stale source/time", entry)
			}
			// The first successful record atomically registered the actual key.
			// From this boundary onward, actual-only clears fence this admission.
			store.Clear(statistics.StatementFilter{UserID: actual.UserID}, false, now.Add(3*time.Second))
			fresh := store.Begin(actual, actual, "new history", now.Add(4*time.Second))
			store.RecordExecution(fresh, statistics.StatementRecord{Key: actual, Query: "new raw", At: now.Add(4 * time.Second), Rows: 1, Milliseconds: 1})
			store.Release(fresh)
			store.RecordExecution(token, record)
			entry = statementEntryFor(t, store, actual)
			if entry.Execution.Count != 1 || entry.Rows != 1 || entry.Query != "new history" {
				t.Fatal("post-registration actual-key clear did not fence old work", entry)
			}
		})
	}
}

func TestPgStatStatementsClearAdmissionMinmaxKeys(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clearUser  uint32
		actualUser uint32
		fenced     bool
	}{
		{"original_only", 10, 20, false},
		{"known_result", 20, 20, true},
		{"different_candidate", 20, 30, false},
		{"unknown_result_before_registration", 30, 30, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			original := statementKey(1, 1)
			candidate, actual := original, original
			candidate.UserID, actual.UserID = 20, tc.actualUser
			token := store.Begin(original, candidate, "normalized admission", now)
			defer store.Release(token)
			store.Release(store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now))
			store.Clear(statistics.StatementFilter{UserID: tc.clearUser}, true, now.Add(time.Second))
			at := now.Add(2 * time.Second)
			store.RecordExecution(token, statistics.StatementRecord{Key: actual, Query: "cleaned raw source", At: at, Rows: 3, Milliseconds: 6})
			entry := statementEntryFor(t, store, actual)
			extrema := float64(6)
			if tc.fenced {
				extrema = 0
			}
			if entry.Execution.Count != 1 || entry.Execution.Total != 6 || entry.Rows != 3 || entry.Execution.Min != extrema || entry.Execution.Max != extrema || entry.Query != "cleaned raw source" || entry.StatsSince != at {
				t.Fatal("minmax fence was transferred between different principal keys", tc, entry)
			}
			// Actual registration must preserve its own later minmax fence while
			// still accepting cumulative samples from this live admission.
			store.Clear(statistics.StatementFilter{UserID: actual.UserID}, true, now.Add(3*time.Second))
			store.RecordExecution(token, statistics.StatementRecord{Key: actual, Query: "ignored raw", At: now.Add(4 * time.Second), Rows: 1, Milliseconds: 10})
			entry = statementEntryFor(t, store, actual)
			if entry.Execution.Count != 2 || entry.Execution.Total != 16 || entry.Rows != 4 || entry.Execution.Min != 0 || entry.Execution.Max != 0 || entry.MinMaxStatsSince != now.Add(3*time.Second) {
				t.Fatal("actual-key minmax registration lost its new baseline", entry)
			}
		})
	}
}

func TestPgStatStatementsAccumulatorRelease(t *testing.T) {
	for _, phase := range []string{"failed", "planning_only", "successful"} {
		t.Run(phase, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			key := statementKey(1, 1)
			token := store.Begin(key, key, "normalized", now)
			record := statistics.StatementRecord{Key: key, Query: "raw", At: now, Rows: 3, Milliseconds: 2}
			if phase != "failed" {
				store.RecordPlan(token, record)
			}
			if phase == "successful" {
				store.RecordExecution(token, record)
			}
			before := store.Snapshot()
			store.Release(token)
			store.Release(token)
			store.RecordPlan(token, record)
			store.RecordExecution(token, record)
			if !reflect.DeepEqual(store.Snapshot(), before) {
				t.Fatal("released admission accepted another successful phase", phase, store.Snapshot(), before)
			}
			store.Release(store.Begin(statementKey(2, 2), statementKey(2, 2), "eviction", now))
			info := store.Info()
			store.RecordPlan(token, record)
			store.RecordExecution(token, record)
			if len(store.Snapshot()) != 0 || store.Info() != info {
				t.Fatal("released admission allocated post-eviction history", store.Snapshot(), store.Info())
			}
			fresh := store.Begin(key, key, "fresh", now.Add(time.Second))
			store.RecordExecution(fresh, record)
			store.Release(fresh)
			entry := statementEntryFor(t, store, key)
			if entry.Execution.Count != 1 || entry.Rows != 3 || entry.Planning.Count != 0 || entry.Query != "fresh" {
				t.Fatal("released predecessor affected a new admission", entry)
			}
		})
	}
}

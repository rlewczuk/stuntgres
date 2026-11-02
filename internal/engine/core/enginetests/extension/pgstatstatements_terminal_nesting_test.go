package extension_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCompletionTerminalNesting(t *testing.T) {
	for _, boundary := range []string{"sql_close", "commit"} {
		for _, track := range []string{"top", "all"} {
			t.Run(boundary+"/"+track, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE terminal_nesting_data(k integer)", "INSERT INTO terminal_nesting_data VALUES(1),(2)", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track='all'", "BEGIN"} {
					pgssOwnerExec(t, e, s, q)
				}
				const marker = "phase_terminal_nesting"
				pgssOwnerExec(t, e, s, "DECLARE terminal_nesting_cursor CURSOR FOR SELECT /*"+marker+"*/ k FROM terminal_nesting_data ORDER BY k")
				cursor := core.PayloadOf(s).Cursors.Entries["terminal_nesting_cursor"]
				if cursor == nil || cursor.Root == nil || cursor.Source == nil {
					t.Fatal("DECLARE lacks its actual bound cursor resource")
				}
				root, source := cursor.Root, cursor.Source
				planned := pgssPhaseEntry(t, e, marker)
				if planned.Key.TopLevel || planned.Key.QueryID == 0 || planned.Planning.Count != 1 || planned.Execution.Count != 0 || planned.Rows != 0 {
					t.Fatal("utility-contained planning provenance", planned)
				}
				check := func(nestedCalls, topCalls int64) {
					t.Helper()
					entries := make(map[bool]statistics.StatementEntry)
					for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
						if !strings.Contains(entry.Query, marker) {
							continue
						}
						if entry.Key.UserID != planned.Key.UserID || entry.Key.DatabaseID != planned.Key.DatabaseID || entry.Key.QueryID != planned.Key.QueryID {
							t.Fatal("terminal changed non-nesting identity", entry, planned)
						}
						if _, duplicate := entries[entry.Key.TopLevel]; duplicate {
							t.Fatal("split same-level terminal identity", entry)
						}
						entries[entry.Key.TopLevel] = entry
					}
					nested, ok := entries[false]
					if !ok || nested.Planning.Count != 1 || nested.Execution.Count != nestedCalls || nested.Rows != nestedCalls {
						t.Fatal("nested planning/execution terminal", nested, nestedCalls)
					}
					top, present := entries[true]
					if topCalls == 0 {
						if present || len(entries) != 1 {
							t.Fatal("unexpected top-level terminal", entries)
						}
					} else if !present || len(entries) != 2 || top.Planning.Count != 0 || top.Execution.Count != topCalls || top.Rows != topCalls {
						t.Fatal("top terminal did not split from nested plan", entries, topCalls)
					}
				}
				check(0, 0)
				fetched, err := pgssOwnerSQL(t, e, s, "FETCH FORWARD 1 FROM terminal_nesting_cursor")
				if err != nil || fetched.Count != 1 || fetched.UtilityRows != 1 || cursor.Root != root || cursor.Source != source {
					t.Fatal("FETCH replaced or failed its retained real resource", fetched.Count, fetched.UtilityRows, err)
				}
				check(0, 0)
				held := e.Cluster.StatementStatistics.Snapshot()
				pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='"+track+"'")
				nestedCalls, topCalls := int64(0), int64(0)
				if boundary == "sql_close" {
					pgssOwnerExec(t, e, s, "CLOSE terminal_nesting_cursor")
					if track == "all" {
						nestedCalls = 1
					}
					check(nestedCalls, 0)
					pgssOwnerExec(t, e, s, "COMMIT")
				} else {
					pgssOwnerExec(t, e, s, "COMMIT")
					topCalls = 1
				}
				check(nestedCalls, topCalls)
				if core.PayloadOf(s).Cursors.Entries["terminal_nesting_cursor"] != nil || cursor.Root != nil || cursor.Source != nil {
					t.Fatal("terminal retained its real cursor/graph/source")
				}
				for _, entry := range held {
					if strings.Contains(entry.Query, marker) && (entry.Planning.Count != 1 || entry.Execution.Count != 0 || entry.Rows != 0 || entry.Key.TopLevel) {
						t.Fatal("terminal mutated the held nested planning snapshot", entry)
					}
				}
				pgssOwnerExec(t, e, s, "SELECT 7 AS phase_after_nesting_disposal")
				check(nestedCalls, topCalls)
			})
		}
	}
}

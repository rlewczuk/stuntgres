package extension_test

import (
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
)

func TestPgStatStatementsCompletionSQLCursor(t *testing.T) {
	for _, utility := range []bool{false, true} {
		t.Run(map[bool]string{false: "child", true: "utility"}[utility], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_cursor(k integer)", "INSERT INTO phase_cursor VALUES(1),(2)", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track='all'", "BEGIN"} {
				pgssOwnerExec(t, e, s, q)
			}
			if utility {
				pgssOwnerExec(t, e, s, "SET pg_stat_statements.track_utility=on")
			}
			pgssOwnerExec(t, e, s, "DECLARE phase_cursor_live CURSOR FOR SELECT /*cursor_live 雪*/ k FROM phase_cursor ORDER BY k")
			cursor := core.PayloadOf(s).Cursors.Entries["phase_cursor_live"]
			if cursor == nil || cursor.Root == nil || cursor.Source == nil {
				t.Fatal("DECLARE did not retain the actual accepted producer")
			}
			plans, calls := int64(1), int64(0)
			if utility {
				plans, calls = 0, 1
			}
			pgssPhaseCounts(t, e, "cursor_live 雪", plans, calls, 0)
			fetched, err := pgssOwnerSQL(t, e, s, "FETCH NEXT FROM phase_cursor_live")
			if err != nil || fetched.Count != 1 || fetched.UtilityRows != 1 {
				t.Fatal("FETCH completion rows", fetched.Count, fetched.UtilityRows, err)
			}
			pgssPhaseCounts(t, e, "cursor_live 雪", plans, calls, 0)
			held := e.Cluster.StatementStatistics.Snapshot()
			pgssOwnerExec(t, e, s, "CLOSE phase_cursor_live")
			rows := int64(1)
			if utility {
				rows = 0
			}
			pgssPhaseCounts(t, e, "cursor_live 雪", plans, 1, rows)
			if core.PayloadOf(s).Cursors.Entries["phase_cursor_live"] != nil || cursor.Root != nil || cursor.Source != nil {
				t.Fatal("normal close retained a live executor")
			}
			for _, entry := range held {
				if entry.Query == pgssPhaseEntry(t, e, "cursor_live 雪").Query && (entry.Execution.Count != calls || entry.Rows != 0) {
					t.Fatal("cursor disposal mutated an owned snapshot", entry)
				}
			}
			if !utility && pgssPhaseEntry(t, e, "cursor_live 雪").Key.TopLevel {
				t.Fatal("disabled utility promoted its executor child")
			}
			pgssOwnerExec(t, e, s, "COMMIT")
			pgssPhaseCounts(t, e, "cursor_live 雪", plans, 1, rows)
		})
	}
}

func TestPgStatStatementsCompletionPLCursor(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		calls, rows int64
		state       string
	}{
		{"unused", "OPEN c; CLOSE c;", 1, 0, ""},
		{"partial", "OPEN c; FETCH c INTO x; CLOSE c;", 1, 1, ""},
		{"move", "OPEN c; MOVE FORWARD 2 FROM c; CLOSE c;", 1, 2, ""},
		{"absolute", "OPEN c; FETCH ABSOLUTE 2 FROM c INTO x; CLOSE c;", 1, 2, ""},
		{"completed_child_failed_parent", "OPEN c; FETCH c INTO x; CLOSE c; RAISE EXCEPTION 'parent failed';", 1, 1, "P0001"},
		{"unfinished_failed_parent", "OPEN c; FETCH c INTO x; RAISE EXCEPTION 'parent failed';", 0, 0, "P0001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_pl_cursor(k integer)", "INSERT INTO phase_pl_cursor VALUES(1),(2)", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track='all'"} {
				pgssOwnerExec(t, e, s, q)
			}
			_, err := pgssOwnerSQL(t, e, s, "DO $$DECLARE c CURSOR FOR SELECT /*pl_cursor 雪*/ k FROM phase_pl_cursor ORDER BY k; x integer; BEGIN "+tc.body+" END$$")
			if tc.state == "" {
				if err != nil {
					t.Fatal("successful cursor invocation", err)
				}
			} else {
				enginetest.State(t, err, tc.state)
			}
			entry := pgssPhaseEntry(t, e, "pl_cursor 雪")
			if entry.Query != "SELECT /*pl_cursor 雪*/ k FROM phase_pl_cursor ORDER BY k" || entry.Key.TopLevel {
				t.Fatal("cursor did not retain its own analyzed source/level", entry)
			}
			pgssPhaseCounts(t, e, "pl_cursor 雪", 1, tc.calls, tc.rows)
		})
	}
}

func TestPgStatStatementsCompletionCursorFailure(t *testing.T) {
	for _, pl := range []bool{false, true} {
		t.Run(map[bool]string{false: "sql", true: "pl"}[pl], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_cursor_failure(k integer)", "INSERT INTO phase_cursor_failure VALUES(1),(2)", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track='all'"} {
				pgssOwnerExec(t, e, s, q)
			}
			// Keep sorting inside an OFFSET boundary so projection of the second
			// tuple cannot fail while the first FETCH orders its input.
			query := "SELECT /*cursor_failure 雪*/ 10/(k-2) FROM (SELECT k FROM phase_cursor_failure ORDER BY k OFFSET 0) AS ordered"
			if pl {
				pgssOwnerExec(t, e, s, "DO $$DECLARE c CURSOR FOR "+query+"; x integer; BEGIN OPEN c; FETCH c INTO x; BEGIN FETCH c INTO x; EXCEPTION WHEN division_by_zero THEN END; CLOSE c; END$$")
			} else {
				pgssOwnerExec(t, e, s, "BEGIN")
				pgssOwnerExec(t, e, s, "DECLARE failing_cursor CURSOR FOR "+query)
				first, err := pgssOwnerSQL(t, e, s, "FETCH NEXT FROM failing_cursor")
				if err != nil || first.Count != 1 {
					t.Fatal("first FETCH must produce one tuple before the later failure", first.Count, err)
				}
				_, err = pgssOwnerSQL(t, e, s, "FETCH NEXT FROM failing_cursor")
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
				pgssOwnerExec(t, e, s, "ROLLBACK")
			}
			// The first real tuple was produced, but the same executor later failed.
			// Neither caught error disposal nor explicit transaction cleanup can turn
			// that failed frame into a successful partial execution.
			pgssPhaseCounts(t, e, "cursor_failure 雪", 1, 0, 0)
			if len(core.PayloadOf(s).Cursors.Entries) != 0 {
				t.Fatal("failed cursor retained a registered resource")
			}
		})
	}
}

func TestPgStatStatementsCompletionCursorHold(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE phase_cursor_hold(k integer)", "INSERT INTO phase_cursor_hold VALUES(1),(2)", "SET pg_stat_statements.track_planning=on", "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track='all'", "BEGIN", "DECLARE holding_cursor CURSOR WITH HOLD FOR SELECT /*cursor_hold 雪*/ k FROM phase_cursor_hold ORDER BY k"} {
		pgssOwnerExec(t, e, s, q)
	}
	pgssPhaseCounts(t, e, "cursor_hold 雪", 1, 0, 0)
	planned := pgssPhaseEntry(t, e, "cursor_hold 雪")
	if planned.Key.TopLevel {
		t.Fatal("HOLD planning escaped its DECLARE utility context", planned)
	}
	check := func() {
		t.Helper()
		groups := 0
		var plans, calls, rows int64
		for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
			if entry.Key.UserID != planned.Key.UserID || entry.Key.DatabaseID != planned.Key.DatabaseID || entry.Key.QueryID != planned.Key.QueryID {
				continue
			}
			groups++
			plans += entry.Planning.Count
			calls += entry.Execution.Count
			rows += entry.Rows
			if entry.Key.TopLevel {
				if entry.Planning.Count != 0 || entry.Execution.Count != 1 || entry.Rows != 2 {
					t.Fatal("HOLD command-end executor phase", entry)
				}
			} else if entry.Planning.Count != 1 || entry.Execution.Count != 0 || entry.Rows != 0 {
				t.Fatal("HOLD utility-contained planning phase", entry)
			}
			for _, sample := range []statistics.StatementMoments{entry.Planning, entry.Execution} {
				if math.IsNaN(sample.Total) || math.IsInf(sample.Total, 0) || sample.Total < 0 || sample.Min < 0 || sample.Max < sample.Min || sample.Mean < sample.Min || sample.Mean > sample.Max || sample.Stddev() < 0 {
					t.Fatal("non-real or incoherent HOLD measurement", sample)
				}
			}
		}
		if groups != 2 || plans != 1 || calls != 1 || rows != 2 {
			t.Fatal("HOLD changed combined phases/rows", groups, plans, calls, rows)
		}
	}
	pgssOwnerExec(t, e, s, "COMMIT")
	check()
	cursor := core.PayloadOf(s).Cursors.Entries["holding_cursor"]
	if cursor == nil || cursor.Root != nil || cursor.Source != nil {
		t.Fatal("HOLD did not retire its real executor")
	}
	fetched, err := pgssOwnerSQL(t, e, s, "FETCH ALL FROM holding_cursor")
	if err != nil || fetched.Count != 2 || fetched.UtilityRows != 2 {
		t.Fatal("held FETCH completion rows", fetched.Count, fetched.UtilityRows, err)
	}
	pgssOwnerExec(t, e, s, "CLOSE holding_cursor")
	check()
}

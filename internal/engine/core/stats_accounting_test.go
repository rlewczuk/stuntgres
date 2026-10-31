package core_test

import (
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"testing"
)

func TestStatsSnapshotTransactionAccounting(t *testing.T) {
	cases := []struct {
		name     string
		commands []string
		want     [7]int64
		rows     []int64
	}{
		{"truncate_abort", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(3)", "ROLLBACK"}, [7]int64{2, 0, 0, 0, 2, 0, 2}, nil},
		{"unreleased_child_abort", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "SAVEPOINT a", "INSERT INTO stats_account VALUES(4),(5),(6)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(7)", "ROLLBACK"}, [7]int64{5, 0, 0, 0, 5, 0, 5}, nil},
		{"unreleased_child_commit", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "SAVEPOINT a", "INSERT INTO stats_account VALUES(4),(5),(6)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(7)", "COMMIT"}, [7]int64{1, 0, 0, 1, 0, 1, 1}, []int64{7}},
		{"release_parent_abort", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "SAVEPOINT a", "SAVEPOINT b", "INSERT INTO stats_account VALUES(4),(5),(6)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(7)", "RELEASE a", "ROLLBACK"}, [7]int64{2, 0, 0, 0, 2, 0, 2}, nil},
		{"release_missing_parent_abort", []string{"BEGIN", "SAVEPOINT a", "SAVEPOINT b", "INSERT INTO stats_account VALUES(4),(5),(6)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(7)", "RELEASE a", "ROLLBACK"}, [7]int64{3, 0, 0, 0, 3, 0, 3}, nil},
		{"repeated_truncated_rollback", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "SAVEPOINT a", "INSERT INTO stats_account VALUES(4),(5),(6)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(7)", "ROLLBACK TO a", "INSERT INTO stats_account VALUES(8)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(9),(10)", "ROLLBACK TO a", "COMMIT"}, [7]int64{6, 0, 0, 2, 4, 2, 6}, []int64{1, 2}},
		{"update_delete_truncate_commit", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2),(3)", "UPDATE stats_account SET id=11 WHERE id=1", "DELETE FROM stats_account WHERE id=2", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(3),(4)", "UPDATE stats_account SET id=13 WHERE id=3", "DELETE FROM stats_account WHERE id=4", "COMMIT"}, [7]int64{2, 1, 1, 1, 2, 4, 2}, []int64{13}},
		{"update_delete_truncate_abort", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2),(3)", "UPDATE stats_account SET id=11 WHERE id=1", "DELETE FROM stats_account WHERE id=2", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(3),(4)", "UPDATE stats_account SET id=13 WHERE id=3", "DELETE FROM stats_account WHERE id=4", "ROLLBACK"}, [7]int64{3, 1, 1, 0, 4, 0, 3}, nil},
		{"pl_child_truncate_abort", []string{"DO $$BEGIN INSERT INTO stats_account VALUES(1),(2); BEGIN INSERT INTO stats_account VALUES(4),(5),(6); TRUNCATE stats_account; INSERT INTO stats_account VALUES(7); RAISE EXCEPTION 'discard'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END$$"}, [7]int64{5, 0, 0, 2, 3, 2, 5}, []int64{1, 2}},
		{"pl_successful_child_then_abort", []string{"BEGIN", "DO $$BEGIN INSERT INTO stats_account VALUES(1),(2); BEGIN INSERT INTO stats_account VALUES(4),(5),(6); TRUNCATE stats_account; INSERT INTO stats_account VALUES(7); EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END$$", "ROLLBACK"}, [7]int64{2, 0, 0, 0, 2, 0, 2}, nil},
		{"pl_internal_rollback_first_truncate", []string{"DO $$BEGIN INSERT INTO stats_account VALUES(1),(2); TRUNCATE stats_account; INSERT INTO stats_account VALUES(3); BEGIN PERFORM 1; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; INSERT INTO stats_account VALUES(4),(5),(6); TRUNCATE stats_account; INSERT INTO stats_account VALUES(7); ROLLBACK; END$$"}, [7]int64{2, 0, 0, 0, 2, 0, 2}, nil},
		{"pl_repeated_truncate_commit", []string{"DO $$BEGIN INSERT INTO stats_account VALUES(1),(2); TRUNCATE stats_account; INSERT INTO stats_account VALUES(3); TRUNCATE stats_account; INSERT INTO stats_account VALUES(4),(5),(6); END$$"}, [7]int64{3, 0, 0, 3, 0, 3, 3}, []int64{4, 5, 6}},
		{"captured_policy_before_abort", []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "SET track_counts=off", "INSERT INTO stats_account VALUES(3)", "TRUNCATE stats_account", "SET track_counts=on", "INSERT INTO stats_account VALUES(4)", "SET track_counts=off", "ROLLBACK"}, [7]int64{3, 0, 0, 0, 3, 0, 3}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			writer := enginetest.StartSession(t, e, nil)
			observer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE stats_account(id integer)")
			key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_account")}
			for _, q := range tc.commands {
				enginetest.SessionSQL(t, writer, q)
			}
			statsEnd(t, writer)
			f := e.Statistics.Read(key)
			got := [7]int64{f.Inserts, f.Updates, f.Deletes, f.Live, f.Dead, f.Modifications, f.InsertsSince}
			if got != tc.want {
				t.Fatalf("reporting got %v want %v", got, tc.want)
			}
			var want [][]driver.Value
			for _, id := range tc.rows {
				want = append(want, []driver.Value{id})
			}
			rows := enginetest.Tuples(enginetest.SessionSQL(t, observer, "SELECT id FROM stats_account ORDER BY id"))
			if len(rows) != len(want) || len(rows) != 0 && !reflect.DeepEqual(rows, want) {
				t.Fatalf("physical rows got %v want %v", rows, want)
			}
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared_child_abort", true: "prepared_child_commit"}[commit], func(t *testing.T) {
			e := engine.New()
			writer := enginetest.StartSession(t, e, nil)
			finisher := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE stats_account(id integer)")
			key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_account")}
			for _, q := range []string{"BEGIN", "INSERT INTO stats_account VALUES(1),(2)", "SAVEPOINT child", "INSERT INTO stats_account VALUES(4),(5),(6)", "TRUNCATE stats_account", "INSERT INTO stats_account VALUES(7)", "PREPARE TRANSACTION 'stats_account'"} {
				enginetest.SessionSQL(t, writer, q)
			}
			entry := core.EnginePreparedRef(e).Lookup("stats_account")
			if entry == nil || entry.Stats[key].Level != 0 || entry.Stats[key].BeforeTruncate.Inserts != 2 || entry.Stats[key].Inserts != 1 {
				t.Fatal("PREPARE lost resolved-child/private truncate ownership", entry)
			}
			statsEnd(t, writer)
			if f := e.Statistics.Read(key); f.Inserts != 0 || f.InsertsSince != 0 || f.Live != 0 || f.Dead != 0 {
				t.Fatal("PREPARE exit reported unresolved counters", f)
			}
			end := "ROLLBACK PREPARED 'stats_account'"
			want := [7]int64{2, 0, 0, 0, 2, 0, 2}
			if commit {
				end = "COMMIT PREPARED 'stats_account'"
				want = [7]int64{1, 0, 0, 1, 0, 1, 1}
			}
			enginetest.SessionSQL(t, finisher, end)
			statsEnd(t, finisher)
			f := e.Statistics.Read(key)
			if got := [7]int64{f.Inserts, f.Updates, f.Deletes, f.Live, f.Dead, f.Modifications, f.InsertsSince}; got != want || entry.Stats != nil {
				t.Fatal("prepared one-time settlement", got, want, entry.Stats)
			}
		})
	}
}

// A nested BEFORE TRUNCATE routine must borrow a fresh manifest and return
// the suspended outer command's exact admitted storage/selection authority.
func statsTruncateCommandOwnership(t *testing.T) {
	e := engine.New()
	setup := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE stats_truncate_outer(id integer)",
		"CREATE TABLE stats_truncate_inner(id integer)",
		"INSERT INTO stats_truncate_outer VALUES(1),(2)",
		"INSERT INTO stats_truncate_inner VALUES(3),(4),(5)",
		"CREATE FUNCTION stats_truncate_nested() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN TRUNCATE stats_truncate_inner; RETURN NULL; END$$",
		"CREATE TRIGGER stats_truncate_nested BEFORE TRUNCATE ON stats_truncate_outer FOR EACH STATEMENT EXECUTE FUNCTION stats_truncate_nested()",
	} {
		enginetest.SessionSQL(t, setup, q)
	}
	outer := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_truncate_outer")}
	inner := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_truncate_inner")}
	statsEnd(t, setup)
	actor := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, actor, "TRUNCATE stats_truncate_outer")
	statsEnd(t, actor)
	observer := enginetest.StartSession(t, e, nil)
	for _, tc := range []struct {
		name    string
		key     statistics.Key
		inserts int64
	}{
		{"stats_truncate_outer", outer, 2},
		{"stats_truncate_inner", inner, 3},
	} {
		if rows := enginetest.Tuples(enginetest.SessionSQL(t, observer, "SELECT id FROM "+tc.name)); len(rows) != 0 {
			t.Fatalf("%s retained rows: %v", tc.name, rows)
		}
		f := e.Statistics.Read(tc.key)
		if f.Inserts != tc.inserts || f.Modifications != tc.inserts || f.Live != 0 || f.Dead != 0 || f.InsertsSince != 0 {
			t.Fatalf("%s nested truncate facts: %+v", tc.name, f)
		}
	}
}

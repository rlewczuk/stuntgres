package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumVisibility(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a := vacuumConn(t, p, b)
		w := vacuumConn(t, p, b)
		run := func(s fixture.Step) { vacuumRun(t, p, b, w, s) }
		for _, q := range []string{"CREATE TABLE vacuum_anchor(id INT)", "CREATE TABLE vacuum_versions(id INT)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO vacuum_anchor VALUES(9)", 1))
		run(fixture.Exec("INSERT INTO vacuum_versions VALUES(1),(2)", 2))
		vacuumRun(t, p, b, a, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		vacuumRows(t, p, b, a, "vacuum_anchor", 9)
		run(fixture.Exec("DELETE FROM vacuum_versions WHERE id=1", 1))
		run(fixture.Exec("VACUUM (FREEZE) vacuum_versions", 0))
		// The target was not queried when the stable snapshot was established.
		vacuumRows(t, p, b, a, "vacuum_versions", 1, 2)
		vacuumRows(t, p, b, w, "vacuum_versions", 2)
		vacuumCount(t, p.Ctx, w, "vacuum_versions", 1)
		run(fixture.Select("SELECT n_live_tup,n_dead_tup FROM pg_stat_all_tables WHERE relid='vacuum_versions'::regclass", []string{"n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		vacuumRun(t, p, b, a, fixture.Exec("COMMIT", 0))
		run(fixture.Exec("VACUUM vacuum_versions", 0))
		vacuumRows(t, p, b, a, "vacuum_versions", 2)
		vacuumCount(t, p.Ctx, w, "vacuum_versions", 2)
		run(fixture.Select("SELECT n_dead_tup FROM pg_stat_all_tables WHERE relid='vacuum_versions'::regclass", []string{"n_dead_tup"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, finish := range []string{"ROLLBACK", "COMMIT"} {
			vacuumRun(t, p, b, a, fixture.Exec("BEGIN", 0))
			vacuumRun(t, p, b, a, fixture.Exec("DELETE FROM vacuum_versions WHERE id=2", 1))
			run(fixture.Exec("VACUUM vacuum_versions", 0))
			vacuumRows(t, p, b, w, "vacuum_versions", 2)
			vacuumRows(t, p, b, a, "vacuum_versions")
			vacuumRun(t, p, b, a, fixture.Exec(finish, 0))
			if finish == "ROLLBACK" {
				vacuumRows(t, p, b, w, "vacuum_versions", 2)
			} else {
				vacuumRows(t, p, b, w, "vacuum_versions")
			}
		}
		run(fixture.Exec("INSERT INTO vacuum_versions VALUES(2)", 1))
		vacuumRun(t, p, b, a, fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
		vacuumRows(t, p, b, a, "vacuum_versions", 2)
		run(fixture.Exec("INSERT INTO vacuum_versions VALUES(3)", 1))
		run(fixture.Exec("VACUUM vacuum_versions", 0))
		vacuumRows(t, p, b, a, "vacuum_versions", 2, 3)
		vacuumRun(t, p, b, a, fixture.Exec("COMMIT", 0))
		for _, prepared := range []bool{false, true} {
			q := "SELECT id FROM vacuum_versions ORDER BY id"
			var rows *sql.Rows
			var err error
			if prepared {
				h := vacuumHeld(t, p, a, q)
				rows, err = h.Statement.QueryContext(p.Ctx)
			} else {
				rows, err = a.QueryContext(p.Ctx, q)
			}
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer rows.Close()
				run(fixture.Exec("DELETE FROM vacuum_versions WHERE id=2", 1))
				run(fixture.Exec("VACUUM (FREEZE,TRUNCATE false) vacuum_versions", 0))
				vacuumRows(t, p, b, w, "vacuum_versions", 3)
				for _, want := range []int{2, 3} {
					if !rows.Next() {
						t.Fatal("lost retained result", rows.Err())
					}
					var got int
					if err := rows.Scan(&got); err != nil || got != want {
						t.Fatalf("owned result=%d err=%v want=%d", got, err, want)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra/failed retained result", rows.Err())
				}
			}()
			run(fixture.Exec("INSERT INTO vacuum_versions VALUES(2)", 1))
		}
		if !b.PG {
			// Native controls have no PostgreSQL API counterpart.
			owned, err := a.QueryContext(p.Ctx, "SELECT id FROM vacuum_versions ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer owned.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("VACUUM (FREEZE) vacuum_versions", 0))
			vacuumRows(t, p, b, w, "vacuum_versions", 2, 3)
			for _, want := range []int{2, 3} {
				var got int
				if !owned.Next() {
					t.Fatal("control invalidated owned rows")
				}
				if err := owned.Scan(&got); err != nil || got != want {
					t.Fatalf("retained control result %d %v", got, err)
				}
			}
			if owned.Next() || owned.Err() != nil {
				t.Fatal("control retained result", owned.Err())
			}
		}
	})
}

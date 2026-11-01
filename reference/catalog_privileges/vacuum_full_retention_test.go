package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumFullRetention(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE full_retained(id INT PRIMARY KEY, v TEXT)", 0))
		f.run(fixture.Exec("INSERT INTO full_retained VALUES(1,'old'),(2,'two')", 2))
		f.run(fixture.Exec("CREATE TABLE full_retention_anchor(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_retention_anchor VALUES(9)", 1))
		reader := vacuumConn(f.t, f.p, f.b)
		vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		defer reader.ExecContext(f.p.Ctx, "ROLLBACK")
		// Assign an actual retained global snapshot without target AS admission.
		vacuumRows(f.t, f.p, f.b, reader, "full_retention_anchor", 9)
		query := "SELECT id,v FROM full_retained ORDER BY id"
		held := vacuumHeld(f.t, f.p, f.c, query)
		detachedConn := vacuumConn(f.t, f.p, f.b)
		rows, err := detachedConn.QueryContext(f.p.Ctx, query)
		if err != nil {
			f.t.Fatal(err)
		}
		defer rows.Close()
		f.run(fixture.Exec("DELETE FROM full_retained WHERE id=1", 1))
		f.run(fixture.Exec("UPDATE full_retained SET v='new' WHERE id=2", 1))
		before := f.locator("full_retained")
		f.command(fixture.Exec("VACUUM FULL full_retained", 0))
		f.rewritten("full_retained", before)
		// A second owner captures the first FULL's incarnation, without taking
		// target AS. Both snapshots must survive DML and another real FULL.
		second := vacuumConn(f.t, f.p, f.b)
		vacuumRun(f.t, f.p, f.b, second, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		defer second.ExecContext(f.p.Ctx, "ROLLBACK")
		vacuumRows(f.t, f.p, f.b, second, "full_retention_anchor", 9)
		f.run(fixture.Exec("UPDATE full_retained SET v='newer' WHERE id=2", 1))
		f.run(fixture.Exec("INSERT INTO full_retained VALUES(3,'three')", 1))
		before = f.locator("full_retained")
		f.command(fixture.Exec("VACUUM FULL full_retained", 0))
		f.rewritten("full_retained", before)
		old := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "two"}})
		vacuumRun(f.t, f.p, f.b, reader, old)
		vacuumRun(f.t, f.p, f.b, second, fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "new"}}))
		var detached [][]any
		for rows.Next() {
			var id int64
			var v string
			if err := rows.Scan(&id, &v); err != nil {
				f.t.Fatal(err)
			}
			detached = append(detached, []any{id, v})
		}
		if err := rows.Err(); err != nil {
			f.t.Fatal(err)
		}
		if !reflect.DeepEqual(detached, old.Rows) {
			f.t.Fatalf("detached result changed: %#v", detached)
		}
		current := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "newer"}, {int64(3), "three"}})
		vacuumRun(f.t, f.p, f.b, held, current)
		vacuumRun(f.t, f.p, f.b, reader, fixture.Exec("ROLLBACK", 0))
		vacuumRun(f.t, f.p, f.b, second, fixture.Exec("ROLLBACK", 0))
		before = f.locator("full_retained")
		f.command(fixture.Exec("VACUUM FULL full_retained", 0))
		f.rewritten("full_retained", before)
		f.run(current)
		f.emptyProgress()
	})
}

package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedPrepared(t *testing.T) {
	t.Run("utility-notice", addGeneratedUtilityNotice)
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.exec("CREATE TABLE g(id integer)", 0)
		g.exec("INSERT INTO g VALUES(1)", 1)
		insert := g.hold("INSERT INTO g(id) VALUES($1)")
		update := g.hold("UPDATE g SET id=$1 WHERE id=$2")
		selectID := g.hold("SELECT id FROM g ORDER BY id")
		star := g.hold("SELECT * FROM g ORDER BY id")
		g.held(star, fixture.Select(star.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		q := "ALTER TABLE g ADD b integer GENERATED ALWAYS AS(id+1) STORED"
		alter := g.hold(q)
		g.run(fixture.Select("SELECT * FROM g", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		g.kinds("g")
		// Native rows are eagerly materialized: retain the actual old cursor over ADD.
		// PostgreSQL streams cursors and retains a heap lock, so consume its rows first.
		old, err := g.c.QueryContext(g.p.Ctx, "SELECT * FROM g ORDER BY id")
		if err != nil {
			g.t.Fatal(err)
		}
		defer old.Close()
		before, err := old.ColumnTypes()
		if err != nil {
			g.t.Fatal(err)
		}
		consumeOld := func() {
			g.t.Helper()
			names, err := old.Columns()
			if err != nil || !reflect.DeepEqual(names, []string{"id"}) {
				g.t.Fatal(names, err)
			}
			after, err := old.ColumnTypes()
			if err != nil || len(after) != 1 || after[0].DatabaseTypeName() != before[0].DatabaseTypeName() {
				g.t.Fatal(after, err)
			}
			if !old.Next() {
				g.t.Fatal("old row missing", old.Err())
			}
			var id int64
			if err := old.Scan(&id); err != nil || id != 1 {
				g.t.Fatal(id, err)
			}
			if old.Next() || old.Err() != nil {
				g.t.Fatal("old cursor changed", old.Err())
			}
			if err := old.Close(); err != nil {
				g.t.Fatal(err)
			}
		}
		if g.b.PG {
			consumeOld()
		}
		g.held(alter, fixture.Exec(q, 0))
		if !g.b.PG {
			consumeOld()
		}
		g.held(insert, fixture.Exec(insert.SQL, 1, 2))
		g.held(update, fixture.Exec(update.SQL, 1, 4, 1))
		g.held(selectID, fixture.Select(selectID.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}))
		read := g.hold("SELECT id,b FROM g ORDER BY id")
		g.held(read, fixture.Select(read.SQL, []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}, {int64(4), int64(5)}}))
		g.held(update, fixture.Exec(update.SQL, 1, 8, 4))
		g.held(read, fixture.Select(read.SQL, []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}, {int64(8), int64(9)}}))
		failure := fixture.ErrorDiagnostic(star.SQL, addGeneratedDiagnostic("0A000", "cached plan must not change result type"))
		failure.SelectRows, failure.ErrorStage = true, "execute"
		g.held(star, failure)
		for _, prep := range []bool{false, true} {
			fixture.TableDDLMetadata(g.t, g.p.Ctx, g.c, g.b.PG, prep, "SELECT id,b FROM g ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 2)
			fixture.TableDDLMetadata(g.t, g.p.Ctx, g.c, g.b.PG, prep, "SELECT id,b FROM g WHERE false", []string{"id", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
		}
		bad := g.hold("ALTER TABLE g ADD bad integer GENERATED ALWAYS AS(id/0) STORED")
		failure = fixture.ErrorDiagnostic(bad.SQL, addGeneratedDiagnostic("22012", "division by zero"))
		failure.ErrorStage = "execute"
		g.held(bad, failure)
		g.kinds("g", []any{"b", "s"})
		g.held(read, fixture.Select(read.SQL, []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}, {int64(8), int64(9)}}))
	})
}

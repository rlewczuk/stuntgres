package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLsegFeatureSmoke(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lseg_smoke(id integer NOT NULL,s lseg)", 0))
		run(fixture.Exec("INSERT INTO lseg_smoke VALUES(1,'[(1,4),(3,2)]'),(2,'((1,4),(3,2))'),(3,'(1,4),(3,2)'),(4,'1,4,3,2'),(5,'[(3,2),(1,4)]')", 5))
		insert := "INSERT INTO lseg_smoke VALUES($1,$2::lseg)"
		ins, err := c.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer ins.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: ins, SQL: insert}, b.Recorder, fixture.Exec(insert, 1, 6, nil))
		q := "SELECT id,s FROM lseg_smoke ORDER BY id"
		want := [][]any{{int64(1), "[(1,4),(3,2)]"}, {int64(2), "[(1,4),(3,2)]"}, {int64(3), "[(1,4),(3,2)]"}, {int64(4), "[(1,4),(3,2)]"}, {int64(5), "[(3,2),(1,4)]"}, {int64(6), nil}}
		run(boxSelect(b, q, []string{"id", "s"}, []string{"INT4", "LSEG"}, want))
		run(fixture.Select(lsegComparison("lseg '1,4,3,2'", "lseg '3,2,1,4'"), []string{"eq", "ne", "lt", "le", "gt", "ge"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true, false, true}}))
		var retained *sql.Rows
		if !b.PG {
			retained, err = c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		run(fixture.Exec("UPDATE lseg_smoke SET s[0]='(-5,-6)'::point WHERE id=1", 1))
		updated := [][]any{{int64(1), "[(-5,-6),(3,2)]"}, {int64(2), "[(1,4),(3,2)]"}, {int64(3), "[(1,4),(3,2)]"}, {int64(4), "[(1,4),(3,2)]"}, {int64(5), "[(3,2),(1,4)]"}, {int64(6), nil}}
		s := boxSelect(b, q, []string{"id", "s"}, []string{"INT4", "LSEG"}, updated)
		run(s)
		run(boxError("SELECT lseg 'bad'", "22P02", "invalid input syntax for type lseg: \"bad\"", "13", "", false, false))
		bind := "SELECT $1::lseg AS s"
		stmt, err := c.PrepareContext(p.Ctx, bind)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: bind}
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type lseg: \"bad\"")
		d.Context = "unnamed portal parameter $1 = '...'"
		bad := fixture.ErrorDiagnostic(bind, d, "bad")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, bind, []string{"s"}, []string{"LSEG"}, [][]any{{"[(3,2),(1,4)]"}}, "3,2,1,4"))
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(s)
		run(fixture.Prepared(s))
		lsegConsumeRetained(t, retained, want)
	})
}

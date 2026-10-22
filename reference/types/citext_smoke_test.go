package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCitextFeatureSmoke(t *testing.T) {
	citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE citext_smoke(id integer NOT NULL,v citext)", 0))
		run(fixture.Exec("CREATE TABLE citext_smoke_empty(v citext NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO citext_smoke VALUES (1,'MiXeD'),(2,NULL),(3,'')", 3))
		run(fixture.Exec("INSERT INTO citext_smoke VALUES ($1,$2)", 1, 4, "  Straße İ Σς é🙂  "))
		q := "INSERT INTO citext_smoke VALUES ($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		buffer := []byte("O'Reilly\\path\nNext")
		var typedNil []byte
		for i, value := range []any{buffer, typedNil, "e\u0301"} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+5, value))
		}
		buffer[0] = 'X'
		wantRows := [][]any{{int64(1), "MiXeD"}, {int64(2), nil}, {int64(3), ""}, {int64(4), "  Straße İ Σς é🙂  "}, {int64(5), "O'Reilly\\path\nNext"}, {int64(6), nil}, {int64(7), "e\u0301"}}
		check := func() {
			s := citextSelect(b, "SELECT id,v FROM citext_smoke ORDER BY id", []string{"id", "v"}, wantRows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { citextMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
			run(s)
			empty := citextSelect(b, "SELECT v FROM citext_smoke_empty", []string{"v"}, [][]any{})
			empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { citextMetadata(t, b.PG, rows, empty.Columns, []bool{false}) }
			run(empty)
		}
		check()
		var retained *sql.Rows
		var retainedMetadata []*sql.ColumnType
		if !b.PG {
			retained, err = c.QueryContext(p.Ctx, "SELECT id,v FROM citext_smoke ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			retainedMetadata = citextMetadata(t, false, retained, []string{"id", "v"}, []bool{false, true})
		}
		q = "INSERT INTO citext_smoke VALUES (8,$1),(9,$2)"
		atomicStmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer atomicStmt.Close()
		atomicHeld := fixture.HeldStatement{Executor: c, Statement: atomicStmt, SQL: q}
		want := citextDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0xff`)
		want.Context = "unnamed portal parameter $2"
		citextError(t, p, b, atomicHeld, q, false, false, false, want, "Valid first", []byte{255})
		check()
		fixture.RunStep(t, p.Ctx, atomicHeld, b.Recorder, fixture.Exec(q, 2, "Recovered", "Recovered later"))
		run(citextSelect(b, "SELECT id,v FROM citext_smoke WHERE id>7 ORDER BY id", []string{"id", "v"}, [][]any{{int64(8), "Recovered"}, {int64(9), "Recovered later"}}))
		run(fixture.Exec("DELETE FROM citext_smoke WHERE id=9", 1))
		wantRows = append(wantRows, []any{int64(8), "Recovered"})
		check()
		if !b.PG {
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
			check()
			citextConsumeRetained(t, retained, wantRows[:7])
			citextColumnMetadata(t, false, retainedMetadata, []string{"id", "v"}, []bool{false, true})
		} else {
			for _, q := range []string{"DROP TABLE citext_smoke", "DROP TABLE citext_smoke_empty", "DROP EXTENSION citext", "CREATE EXTENSION citext", "CREATE TABLE citext_smoke(id integer NOT NULL,v citext)", "CREATE TABLE citext_smoke_empty(v citext NOT NULL)"} {
				run(fixture.Exec(q, 0))
			}
			for _, row := range wantRows {
				run(fixture.Exec("INSERT INTO citext_smoke VALUES ($1,$2)", 1, row...))
			}
			check()
		}
	})
}

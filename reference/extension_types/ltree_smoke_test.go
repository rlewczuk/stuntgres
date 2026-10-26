package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLtreeFeatureSmoke(t *testing.T) {
	ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lt_smoke(id integer NOT NULL,v ltree)", 0))
		run(fixture.Exec("INSERT INTO lt_smoke VALUES (1,'Top.Countries.Europe')", 1))
		q := "INSERT INTO lt_smoke VALUES ($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "42._.-.a-b_c"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, ""))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,4,nil))
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		values := [][]any{{int64(1), "Top.Countries.Europe"}, {int64(2), "42._.-.a-b_c"}, {int64(3), ""}, {int64(4), nil}}
		check := ltreeSelect(b, "SELECT id,v FROM lt_smoke ORDER BY id", []string{"id", "v"}, values)
		check.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ltreeMetadata(t, b.PG, rows, check.Columns, []bool{false, true}) }
		run(check)
		want := queryIntParameterDiagnostic("42601", "ltree syntax error at character 3", "a..b", 1)
		ltreeError(t, p, b, c, "INSERT INTO lt_smoke VALUES (5,'ok'),(6,$1)", false, false, false, want, "a..b")
		run(check)
		want = queryIntParameterDiagnostic("42601","ltree syntax error at character 1","é",1)
		ltreeError(t,p,b,c,"INSERT INTO lt_smoke VALUES (5,$1)",false,false,false,want,"é")
		run(check)
		if !b.PG {
			old, err := b.DB.QueryContext(p.Ctx, check.Query)
			if err != nil { t.Fatal(err) }
			defer old.Close()
			metadata := ltreeMetadata(t, false, old, check.Columns, []bool{false, true})
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			run(check)
			ltreeConsumeRetained(t, old, values)
			ltreeColumnMetadata(t, false, metadata, check.Columns, []bool{false, true})
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual smoke Dump replay", err) }
				pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
				s := ltreeSelect(pg, check.Query, check.Columns, values)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ltreeMetadata(t, true, rows, s.Columns, []bool{false, true}) }
				fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, s)
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
			}
		} else {
			for _, q := range []string{"DROP TABLE lt_smoke", "DROP EXTENSION ltree", "CREATE EXTENSION ltree", "CREATE TABLE lt_smoke(id integer NOT NULL,v ltree)"} { run(fixture.Exec(q, 0)) }
			run(fixture.Exec("INSERT INTO lt_smoke VALUES (1,'Top.Countries.Europe'),(2,'42._.-.a-b_c'),(3,''),(4,NULL)", 4))
			run(check)
		}
	})
}
package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLqueryFeatureSmoke(t *testing.T) {
	lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		lqueryInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lq_smoke(id integer NOT NULL,v lquery)", 0))
		run(fixture.Exec("INSERT INTO lq_smoke VALUES (1,'Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain')", 1))
		q := "INSERT INTO lq_smoke VALUES ($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "foo*@%@@**"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, nil))
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		values := [][]any{{int64(1), "Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain"}, {int64(2), "foo%@*"}, {int64(3), nil}}
		check := lquerySelect(b, "SELECT id,v FROM lq_smoke ORDER BY id", []string{"id", "v"}, values)
		check.CheckMetadata = func(t *testing.T, rows *sql.Rows) { lqueryMetadata(t, b.PG, rows, check.Columns, []bool{false, true}) }
		run(check)
		want := queryIntParameterDiagnostic("42601", "lquery syntax error at character 3", "a..b", 1)
		lqueryError(t, p, b, c, "INSERT INTO lq_smoke VALUES (4,'a'),(5,$1)", false, false, false, want, "a..b")
		run(check)
		if !b.PG {
			old, err := b.DB.QueryContext(p.Ctx, check.Query)
			if err != nil { t.Fatal(err) }
			defer old.Close()
			metadata := lqueryMetadata(t, false, old, check.Columns, []bool{false, true})
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			run(check)
			lqueryConsumeRetained(t, old, values)
			lqueryColumnMetadata(t, false, metadata, check.Columns, []bool{false, true})
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual smoke Dump replay", err) }
				pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
				s := lquerySelect(pg, check.Query, check.Columns, values)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { lqueryMetadata(t, true, rows, s.Columns, []bool{false, true}) }
				fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, s)
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
			}
		} else {
			for _, q := range []string{"DROP TABLE lq_smoke", "DROP EXTENSION ltree", "CREATE EXTENSION ltree", "CREATE TABLE lq_smoke(id integer NOT NULL,v lquery)"} { run(fixture.Exec(q, 0)) }
			run(fixture.Exec("INSERT INTO lq_smoke VALUES (1,'Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain'),(2,'foo%@*'),(3,NULL)", 3))
			run(check)
		}
	})
}

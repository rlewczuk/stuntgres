package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimestamptzRangeDomainControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, q := range []string{"CREATE DOMAIN span AS pg_catalog.tstzrange", "CREATE DOMAIN chain AS span", "CREATE TABLE r(k integer NOT NULL,a span,b chain)", "CREATE TABLE vacant(a span NOT NULL,b chain)", "CREATE TABLE guard(k integer NOT NULL,v chain NOT NULL)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO r VALUES (1,'(2000-01-01,2000-01-02]','[epoch,epoch]'),(2,'empty','[,]'),(3,NULL,NULL),(4,'[epoch,infinity]','[epoch,)')", 4))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 5, nil, []byte(nil))))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 6, []byte("[epoch,epoch]"), "[-infinity,infinity]")))
		want := [][]any{
			{int64(1), `("2000-01-01 00:00:00+00","2000-01-02 00:00:00+00"]`, `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`},
			{int64(2), "empty", "(,)"}, {int64(3), nil, nil},
			{int64(4), `["1970-01-01 00:00:00+00",infinity]`, `["1970-01-01 00:00:00+00",)`},
			{int64(5), nil, nil}, {int64(6), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`, "[-infinity,infinity]"},
		}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "TSTZRANGE", "TSTZRANGE"}, want, []bool{false, true, true}),
				rangeSelect(b, "SELECT a,b FROM vacant", []string{"a", "b"}, []string{"TSTZRANGE", "TSTZRANGE"}, nil, []bool{false, true}),
			} {
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Exec("INSERT INTO guard VALUES (1,'[epoch,epoch]')", 1))
		d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "guard" violates not-null constraint`)
		d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (2, null).", "public", "guard", "v"
		for _, q := range []string{"INSERT INTO guard VALUES (2,$1)", "INSERT INTO guard VALUES (0,'[epoch,epoch]'),(2,$1)"} {
			fixture.CompositeReferenceError(t, p, conn, q, "execute", d, nil)
			run(rangeSelect(b, "SELECT k,v FROM guard", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, [][]any{{int64(1), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`}}, []bool{false, false}))
		}
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		q := "INSERT INTO r VALUES ($1,'[01/02/2003,01/03/2003)',NULL)"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("SET DateStyle = 'ISO, DMY'", 0))
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 7))
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(rangeSelect(b, "SELECT a,b FROM r WHERE k=7", []string{"a", "b"}, []string{"TSTZRANGE", "TSTZRANGE"}, [][]any{{`["2003-01-02 00:00:00+00","2003-01-03 00:00:00+00")`, nil}}, []bool{true, true}))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		want = append(want, []any{int64(7), `["2003-01-02 00:00:00+00","2003-01-03 00:00:00+00")`, nil})
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "TSTZRANGE", "TSTZRANGE"}, want, []bool{false, true, true}),
			rangeSelect(b, "SELECT a,b FROM vacant", []string{"a", "b"}, []string{"TSTZRANGE", "TSTZRANGE"}, [][]any{}, []bool{false, true}),
			rangeSelect(b, "SELECT k,v FROM guard", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, [][]any{{int64(1), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`}}, []bool{false, false}),
		}
		script := "CREATE DOMAIN span AS tstzrange; CREATE DOMAIN chain AS span; CREATE TABLE guard(k integer NOT NULL,v chain NOT NULL); INSERT INTO guard VALUES (1,'[epoch,epoch]'); INSERT INTO guard VALUES (2,NULL);"
		rangeControlCycle(t, p, b, checks, [][]bool{{false, true, true}, {false, true}, {false, false}}, []rangeStagedFailure{{script, d}})
	}
}

func TestReferenceTimestamptzRangeComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, q := range []string{"CREATE DOMAIN span AS tstzrange", "CREATE TYPE envelope AS (a tstzrange,b span)", "CREATE TABLE r(k integer NOT NULL,v envelope)", "CREATE TABLE scalar(k integer NOT NULL,v tstzrange)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO r VALUES (1,'("(2000-01-01,2000-01-02]","[epoch,infinity]")'),(2,'(,)'),(3,NULL)`, 3))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 4, []byte(`("[epoch,epoch]","[epoch,)")`))))
		check := rangeSelect(b, "SELECT k,(v).a AS a,(v).b AS b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "TSTZRANGE", "TSTZRANGE"}, [][]any{
			{int64(1), `("2000-01-01 00:00:00+00","2000-01-02 00:00:00+00"]`, `["1970-01-01 00:00:00+00",infinity]`},
			{int64(2), nil, nil}, {int64(3), nil, nil},
			{int64(4), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`, `["1970-01-01 00:00:00+00",)`},
		}, nil)
		for _, prepared := range []bool{false, true} {
			s := check
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("INSERT INTO scalar VALUES (1,'(2000-01-01,2000-01-02]'),(2,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			check := rangeSelect(b, "SELECT ANY_VALUE(v) AS v FROM scalar", []string{"v"}, []string{"TSTZRANGE"}, [][]any{{`("2000-01-01 00:00:00+00","2000-01-02 00:00:00+00"]`}}, []bool{true})
			check.Prepared = prepared
			run(check)
		}
		rangeControlCycle(t, p, b, []fixture.Step{check}, [][]bool{nil}, nil)
	}
}

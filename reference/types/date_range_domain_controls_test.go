package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Destination-context replay reconstructs plain and nested domain ownership
// without granting non-contextual daterange casts.
func TestReferenceDateRangeDomainControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, query := range []string{
			"CREATE DOMAIN public.span AS pg_catalog.daterange",
			"CREATE DOMAIN public.chain AS public.span",
			"CREATE TABLE r(k integer NOT NULL,a public.span,b public.chain)",
			"CREATE TABLE vacant(a public.span NOT NULL,b public.chain)",
			"CREATE TABLE guard(k integer NOT NULL,v public.chain NOT NULL)",
		} { run(fixture.Exec(query, 0)) }
		run(fixture.Exec("INSERT INTO r VALUES (1,'[2000-02-28,2000-03-01]','(2000-02-28,2000-03-01]'),(2,'empty','[,]'),(3,NULL,NULL),(4,'[2000-01-01,infinity]','[2000-01-01,)')", 4))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 5, nil, []byte(nil))))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 6, []byte("[epoch,epoch]"), "[-infinity,infinity]")))
		want := [][]any{
			{int64(1), "[2000-02-28,2000-03-02)", "[2000-02-29,2000-03-02)"},
			{int64(2), "empty", "(,)"}, {int64(3), nil, nil},
			{int64(4), "[2000-01-01,infinity]", "[2000-01-01,)"},
			{int64(5), nil, nil}, {int64(6), "[1970-01-01,1970-01-02)", "[-infinity,infinity]"},
		}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "DATERANGE", "DATERANGE"}, want, []bool{false, true, true}),
				rangeSelect(b, "SELECT a,b FROM vacant", []string{"a", "b"}, []string{"DATERANGE", "DATERANGE"}, nil, []bool{false, true}),
			} { s.Prepared = prepared; run(s) }
		}
		run(fixture.Exec("INSERT INTO guard VALUES (1,'[2000-02-28,2000-03-01]')", 1))
		wantError := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "guard" violates not-null constraint`)
		wantError.Detail, wantError.Schema, wantError.Table, wantError.Column = "Failing row contains (2, null).", "public", "guard", "v"
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"INSERT INTO guard VALUES (2,$1)", "INSERT INTO guard VALUES (0,'[epoch,epoch]'),(2,$1)"} {
				for _, queryMode := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, wantError, nil)
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
					run(s)
					run(rangeSelect(b, "SELECT k,v FROM guard ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, [][]any{{int64(1), "[2000-02-28,2000-03-02)"}}, []bool{false, false}))
				}
			}
		}
		// Nested-domain contextual literals retain their decoded endpoints too.
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		q := "INSERT INTO r VALUES ($1,'[01/02/03,01/03/03)',NULL)"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		run(fixture.Exec("SET DateStyle = 'ISO, DMY'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 7))
		run(rangeSelect(b, "SELECT a,b FROM r WHERE k=7", []string{"a", "b"}, []string{"DATERANGE", "DATERANGE"}, [][]any{{"[2003-01-02,2003-01-03)", nil}}, []bool{true, true}))
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		if err := conn.Close(); err != nil { t.Fatal(err) }
		want = append(want, []any{int64(7), "[2003-01-02,2003-01-03)", nil})
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "DATERANGE", "DATERANGE"}, want, []bool{false, true, true}),
			rangeSelect(b, "SELECT a,b FROM vacant", []string{"a", "b"}, []string{"DATERANGE", "DATERANGE"}, nil, []bool{false, true}),
			rangeSelect(b, "SELECT k,v FROM guard ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, [][]any{{int64(1), "[2000-02-28,2000-03-02)"}}, []bool{false, false}),
		}
		script := "CREATE DOMAIN public.span AS daterange; CREATE DOMAIN public.chain AS public.span; CREATE TABLE guard(k integer NOT NULL,v public.chain NOT NULL); INSERT INTO guard VALUES (1,'[epoch,epoch]'); INSERT INTO guard VALUES (2,NULL);"
		rangeControlCycle(t, p, b, checks, [][]bool{{false, true, true}, {false, true}, {false, false}}, []rangeStagedFailure{{script, wantError}})
	}
}

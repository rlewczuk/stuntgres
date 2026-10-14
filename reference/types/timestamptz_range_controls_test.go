package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func timestamptzRangeStagedFailures() []rangeStagedFailure {
	var failures []rangeStagedFailure
	for _, tc := range timestamptzRangeFailures() {
		script := "CREATE TABLE staged(v tstzrange); INSERT INTO staged VALUES ('[epoch,epoch]'); INSERT INTO staged VALUES (" + rangeLiteral(tc.input) + ");"
		want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
		want.Detail, want.Hint = tc.detail, tc.hint
		want.Position = strconv.Itoa(utf8.RuneCountInString(script[:strings.LastIndex(script, "('")+1]) + 1)
		failures = append(failures, rangeStagedFailure{script, want})
	}
	return failures
}

func TestReferenceTimestamptzRangeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tstzrange)", 0))
		run(fixture.Exec("CREATE TABLE vacant(v tstzrange NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO r VALUES (1,'(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)'),(2,'[epoch,epoch]'),(3,'empty'),(4,'[,]'),(5,'[-infinity,infinity]'),(6,'[epoch,infinity)'),(7,'[epoch,infinity]'),(8,'[epoch,)'),(9,'[0001-12-31 23:59:59 BC,0001-01-01)'),(10,'(4714-11-24 00:00:00 BC,294276-12-31 23:59:59.999999]'),(11,NULL)", 11))
		want := [][]any{
			{int64(1), `("2000-01-01 00:00:00+00","2000-01-01 00:00:00.000001+00")`},
			{int64(2), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`},
			{int64(3), "empty"}, {int64(4), "(,)"}, {int64(5), "[-infinity,infinity]"},
			{int64(6), `["1970-01-01 00:00:00+00",infinity)`}, {int64(7), `["1970-01-01 00:00:00+00",infinity]`}, {int64(8), `["1970-01-01 00:00:00+00",)`},
			{int64(9), `["0001-12-31 23:59:59+00 BC","0001-01-01 00:00:00+00")`},
			{int64(10), `("4714-11-24 00:00:00+00 BC","294276-12-31 23:59:59.999999+00"]`}, {int64(11), nil},
		}
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, want, []bool{false, true}),
			rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"TSTZRANGE"}, [][]any{}, []bool{false}),
		}
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
		rangeControlCycle(t, p, b, checks, [][]bool{{false, true}, {false}}, timestamptzRangeStagedFailures())
	}
}

func TestReferenceTimestamptzRangeFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tstzrange)", 0))
		run(fixture.Exec("CREATE TABLE vacant(v tstzrange)", 0))
		run(fixture.Exec("CREATE DOMAIN span AS tstzrange", 0))
		run(fixture.Exec("CREATE DOMAIN chain AS span", 0))
		run(fixture.Exec("CREATE TABLE domain_rows(k integer NOT NULL,v chain NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO domain_rows VALUES (1,'[epoch,epoch]')", 1))
		run(fixture.Exec("INSERT INTO r VALUES (8,'[2010-01-01 01:00:00-05,2010-01-01 02:00:00-08)')", 1))
		q := "INSERT INTO r VALUES ($1,$2)"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		inputs := []any{"(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)", "[-infinity,infinity]", "[,]", "empty", nil, []byte(nil), []byte("[epoch,epoch]")}
		want := [][]any{{int64(1), `("2000-01-01 00:00:00+00","2000-01-01 00:00:00.000001+00")`}, {int64(2), "[-infinity,infinity]"}, {int64(3), "(,)"}, {int64(4), "empty"}, {int64(5), nil}, {int64(6), nil}, {int64(7), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`}}
		for i, input := range inputs {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, input))
		}
		for i, tc := range []struct{ input, output string }{
			{"[2004-10-19 10:23:54+02,2004-10-19 08:23:54+00]", `["2004-10-19 08:23:54+00","2004-10-19 08:23:54+00"]`},
			{"[epoch,infinity)", `["1970-01-01 00:00:00+00",infinity)`},
			{"[epoch,infinity]", `["1970-01-01 00:00:00+00",infinity]`},
			{"[epoch,)", `["1970-01-01 00:00:00+00",)`},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+11, tc.input))
		}
		frozenQ := "INSERT INTO r VALUES ($1,'[2020-07-01 12:00,2020-07-01 13:00)')"
		frozen, err := conn.PrepareContext(p.Ctx, frozenQ)
		if err != nil {
			t.Fatal(err)
		}
		defer frozen.Close()
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: frozen, SQL: frozenQ}, b.Recorder, fixture.Exec(frozenQ, 1, 9))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 10, "[2020-07-01 12:00,2020-07-01 13:00)"))
		run(rangeSelect(b, "SELECT v FROM r WHERE k=8", []string{"v"}, []string{"TSTZRANGE"}, [][]any{{`["2010-01-01 01:00:00-05","2010-01-01 05:00:00-05")`}}, []bool{true}))
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		want = append(want, []any{int64(8), `["2010-01-01 06:00:00+00","2010-01-01 10:00:00+00")`}, []any{int64(9), `["2020-07-01 12:00:00+00","2020-07-01 13:00:00+00")`}, []any{int64(10), `["2020-07-01 16:00:00+00","2020-07-01 17:00:00+00")`})
		if err := frozen.Close(); err != nil {
			t.Fatal(err)
		}
		want = append(want, []any{int64(11), `["2004-10-19 08:23:54+00","2004-10-19 08:23:54+00"]`}, []any{int64(12), `["1970-01-01 00:00:00+00",infinity)`}, []any{int64(13), `["1970-01-01 00:00:00+00",infinity]`}, []any{int64(14), `["1970-01-01 00:00:00+00",)`})
		notNull := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "domain_rows" violates not-null constraint`)
		notNull.Detail, notNull.Schema, notNull.Table, notNull.Column = "Failing row contains (2, null).", "public", "domain_rows", "v"
		fixture.CompositeReferenceError(t, p, conn, "INSERT INTO domain_rows VALUES (0,'empty'),(2,$1)", "execute", notNull, nil)
		for _, tc := range []timestamptzRangeFailure{
			{input: "[bad,epoch)", code: "22007", message: `invalid input syntax for type timestamp with time zone: "bad"`},
			{input: "[2010-01-01 01:00:00-08,2010-01-01 02:00:00-05)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Context = `unnamed portal parameter $2 = '...'`
			s := fixture.ErrorDiagnostic("INSERT INTO r VALUES (0,$1),(99,$2)", d, "[epoch,epoch]", tc.input)
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
		}
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, want, []bool{false, true}),
			rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"TSTZRANGE"}, [][]any{}, []bool{true}),
			rangeSelect(b, "SELECT k,v FROM domain_rows", []string{"k", "v"}, []string{"INT4", "TSTZRANGE"}, [][]any{{int64(1), `["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`}}, []bool{false, false}),
		}
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		failures := timestamptzRangeStagedFailures()
		failures = append(failures, rangeStagedFailure{"CREATE DOMAIN span AS tstzrange; CREATE DOMAIN chain AS span; CREATE TABLE domain_rows(k integer NOT NULL,v chain NOT NULL); INSERT INTO domain_rows VALUES (1,'empty'); INSERT INTO domain_rows VALUES (2,NULL);", notNull})
		rangeControlCycle(t, p, b, checks, [][]bool{{false, true}, {true}, {false, false}}, failures)
	}
}

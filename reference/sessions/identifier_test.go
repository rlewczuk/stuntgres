package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierKeywords(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE abort (by INT, between TEXT, values BOOL, integer BIGINT, char TEXT)", 0),
		fixture.Prepared(fixture.Exec("CREATE TABLE insert (by INT)", 0)),
		fixture.Exec("INSERT INTO abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b')", 2),
		fixture.Prepared(fixture.Exec("INSERT INTO public.abort (by,between,values,integer,char) VALUES ($1,$2,$3,$4,$5)", 1, 3, "three", true, 30, "c")),
		fixture.Select("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}),
		fixture.Prepared(fixture.Select("SELECT by,between,values,integer,char FROM abort WHERE by=$1 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}, 2)),
		fixture.Exec("INSERT INTO insert (by) VALUES (7)", 1),
		fixture.Select("SELECT by FROM insert ORDER BY by", []string{"by"}, []string{"INT4"}, [][]any{{int64(7)}}),
		fixture.Exec(`CREATE TABLE "select" ("join" INT,"authorization" TEXT,"a""b" BOOL)`, 0),
		fixture.Exec(`INSERT INTO "select" VALUES (7,'kept',TRUE)`, 1),
		fixture.Select(`SELECT "join","authorization","a""b" FROM "select" ORDER BY "join"`, []string{"join", "authorization", "a\"b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "kept", true}}),
	} {
		p.Step(t, s)
	}
	p.Step(t, fixture.Exec("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	// Restore uses the existing native seam; PostgreSQL executes that exact dump.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE abort,insert,\"select\""); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := stuntgres.Reset(context.Background(), p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("redump differs")
	}
	p.Step(t, fixture.Select("SELECT by,between,values,integer,char FROM abort ORDER BY by", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(1), "one", true, int64(10), "a"}, {int64(2), "two", false, int64(20), "b"}, {int64(3), "three", true, int64(30), "c"}}))
}
func TestReferenceIdentifierDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range identifierSyntaxCases {
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: fmt.Sprintf("syntax error at or near \"%s\"", s.word), Position: s.pos}
		for _, mode := range []string{"exec", "query", "prepare"} {
			step := fixture.ErrorDiagnostic(s.q, want)
			step.ErrorStage = "execute"
			if mode == "query" {
				step.SelectRows = true
			}
			if mode == "prepare" {
				step.Prepared = true
				step.ErrorStage = "prepare"
			}
			p.Step(t, step)
		}
	}
}
func TestReferenceIdentifierLabels(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t (x INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES (7)", 1))
	for _, s := range []struct{ label, column, pos string }{{"check", "check", "10"}, {"default", "default", "10"}, {"authorization", "authorization", "10"}, {"AS char", "char", "13"}, {"AS grant", "grant", "13"}, {"AS \"Mixed\"", "Mixed", "13"}, {`"isnull"`, "isnull", "10"}, {`"notnull"`, "notnull", "10"}} {
		q := "SELECT x " + s.label + " FROM t"
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(q, []string{s.column}, []string{"INT4"}, [][]any{{int64(7)}})
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	for _, word := range []string{"char", "day"} {
		step := fixture.ErrorDiagnostic("SELECT x "+word+" FROM t", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: fmt.Sprintf("syntax error at or near \"%s\"", word), Position: "10"})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}

func TestReferenceIdentifierNormalization(t *testing.T) {
	for _, s := range identifierNormalizationCases {
		t.Run(s.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE "+s.spelling+" ("+s.spelling+" INT)", 0))
			p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO "+s.spelling+" ("+s.spelling+") VALUES ($1)", 1, 7)))
			p.Step(t, fixture.Select("SELECT "+s.spelling+" FROM "+s.spelling+" ORDER BY "+s.spelling, []string{s.name}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceIdentifierQualifiedNames(t *testing.T) {
	p := fixture.NewScenario(t)
	for i, name := range []string{"select", "authorization", "from", "check"} {
		create := fixture.Exec("CREATE TABLE public."+name+" (x INT)", 0)
		if i%2 == 1 {
			create = fixture.Prepared(create)
		}
		p.Step(t, create)
		p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO public."+name+" (x) VALUES ($1)", 1, 7+i)))
		selectStep := fixture.Select("SELECT x FROM public."+name+" WHERE x=$1 ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}, 7+i)
		p.Step(t, selectStep)
		p.Step(t, fixture.Prepared(selectStep))
		p.Step(t, fixture.Select("SELECT x FROM \""+name+"\" ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}))
	}
}

func TestReferenceIdentifierInsertQueries(t *testing.T) {
	for _, s := range []struct {
		q   string
		row any
	}{
		{"INSERT INTO t (SELECT)", nil},
		{"INSERT INTO t SELECT x FROM source_rows", int64(7)},
		{"INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", int64(7)},
		{"INSERT INTO t (x) SELECT x FROM source_rows", int64(7)},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", s.q, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE t (x INT)", 0))
				p.Step(t, fixture.Exec("CREATE TABLE source_rows (x INT)", 0))
				p.Step(t, fixture.Exec("INSERT INTO source_rows VALUES (7)", 1))
				p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, nil))
				step := fixture.Exec(s.q, 1)
				step.Prepared = prepared
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{s.row}}))
			})
		}
	}
}

func TestReferenceIdentifierContextualCapabilities(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t (x INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES (7)", 1))
	for _, tc := range []struct {
		q    string
		want bool
	}{{"SELECT x isnull FROM t", false}, {"SELECT x notnull FROM t", true}} {
		p.Step(t, fixture.Exec(tc.q, 1))
		s := fixture.Select(tc.q, []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, present := range []bool{false, true} {
		if present {
			for _, name := range []string{`f`, `abort`, `authorization`, `"between"`} {
				p.Step(t, fixture.Exec("CREATE FUNCTION public."+name+"(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'", 0))
			}
		}
		for _, tc := range identifierContextCases {
			for _, backend := range p.Backends() {
				for _, route := range []string{"exec", "query", "prepare"} {
					var s fixture.Step
					if present && tc.code == "42883" {
						s = fixture.Select(tc.q, []string{tc.column}, []string{tc.typ}, [][]any{{tc.row}})
						if route == "exec" {
							s = fixture.Exec(tc.q, 1)
						}
						s.Prepared = route == "prepare"
					} else {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Position = tc.position
						if tc.code == "42883" {
							want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						}
						s = fixture.ErrorDiagnostic(tc.q, want)
						s.SelectRows = route == "query"
						s.Prepared = route == "prepare"
						if s.Prepared {
							s.ErrorStage = "prepare"
						} else {
							s.ErrorStage = "execute"
						}
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		}
	}
	p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceIdentifierNullExpressions(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t (x INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES (7),(NULL)", 2))
	for _, s := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT x isnull FROM t ORDER BY x", [][]any{{false}, {true}}},
		{"SELECT x notnull FROM t ORDER BY x", [][]any{{true}, {false}}},
	} {
		step := fixture.Select(s.q, []string{"?column?"}, []string{"BOOL"}, s.rows)
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, s := range []struct {
		q, column, typ string
		rows           [][]any
	}{
		{"SELECT x FROM t ORDER BY x NULLS FIRST", "x", "INT4", [][]any{{nil}, {int64(7)}}},
		{"SELECT x FROM t ORDER BY x DESC NULLS LAST", "x", "INT4", [][]any{{int64(7)}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(s.q, []string{s.column}, []string{s.typ}, s.rows)
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	bad := fixture.ErrorDiagnostic("SELECT x FROM t ORDER BY x NULLS bogus", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"NULLS\"", Position: "28"})
	p.Step(t, bad)
	p.Step(t, fixture.Prepared(bad))
}

func TestReferenceIdentifierLaterProjectionCalls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t (x INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES (7)", 1))
	for _, present := range []bool{false, true} {
		if present {
			for _, name := range []string{`f`, `abort`, `authorization`, `"between"`} {
				p.Step(t, fixture.Exec("CREATE FUNCTION public."+name+"(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'", 0))
			}
		}
		for _, tc := range identifierLaterCallCases {
			for _, backend := range p.Backends() {
				for _, route := range []string{"exec", "query", "prepare"} {
					var s fixture.Step
					if present && tc.code == "42883" {
						s = fixture.Select(tc.q, tc.columns, tc.types, tc.rows)
						if route == "exec" {
							s = fixture.Exec(tc.q, 1)
						}
						s.Prepared = route == "prepare"
					} else {
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Position = tc.position
						if tc.code == "42883" {
							want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						}
						s = fixture.ErrorDiagnostic(tc.q, want)
						s.SelectRows = route == "query"
						s.Prepared = route == "prepare"
						if s.Prepared {
							s.ErrorStage = "prepare"
						} else {
							s.ErrorStage = "execute"
						}
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		}
		q := "CREATE TABLE staged (x INT); SELECT x, f(x) FROM t"
		if present {
			p.Step(t, fixture.Exec(q, 1))
			p.Step(t, fixture.Select("SELECT x FROM staged", []string{"x"}, []string{"INT4"}, nil))
		} else {
			var before, after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			want := fixture.ControlsReferenceDiagnostic("42883", "function f(integer) does not exist")
			want.Position = "40"
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			p.Step(t, fixture.ErrorDiagnostic(q, want))
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("later call published catalog")
			}
		}
	}
	p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

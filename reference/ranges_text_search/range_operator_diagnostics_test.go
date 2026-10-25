package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func operatorDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, want fixture.Diagnostic, stage string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want, args...)
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = stage
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
func operatorMissing(q, left, op, right string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+left+" "+op+" "+right)
	d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	at := strings.Index(q, " "+op+" ") + 1
	if strings.Contains(q, "OPERATOR(") {
		at = strings.Index(q, "OPERATOR(")
	}
	d.Position = timestampRangeComparisonPosition(q, at)
	return d
}
func TestReferenceRangeOperatorDiagnostics(t *testing.T) {
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE op(a int4range,b int8range,m int4multirange,n int8multirange,e integer,s text)", 0))
		run(fixture.Exec("INSERT INTO op VALUES ('[1,3)','[1,3)','{[1,3)}','{[1,3)}',2,'2')", 1))
		for _, tc := range []struct{ left, op, right, lt, rt string }{
			{"a", "@>", "b", "int4range", "int8range"}, {"a", "&&", "n", "int4range", "int8multirange"},
			{"a", "@>", "s", "int4range", "text"}, {"e", "@>", "a", "integer", "int4range"},
			{"a", "&&", "e", "int4range", "integer"}, {"a", "<<", "e", "int4range", "integer"},
			{"a", "+", "m", "int4range", "int4multirange"}, {"m", "-", "a", "int4multirange", "int4range"},
			{"a", "*", "m", "int4range", "int4multirange"}, {"a", "=", "m", "int4range", "int4multirange"},
			{"a", "<", "m", "int4range", "int4multirange"}, {"m", ">=", "a", "int4multirange", "int4range"},
		} {
			q := "SELECT /*é*/ " + tc.left + " " + tc.op + " " + tc.right + " FROM op"
			operatorDiagnostic(t, p, b, q, operatorMissing(q, tc.lt, tc.op, tc.rt), "prepare")
		}
		for _, op := range []string{"@>", "<@", "&&", "<<", ">>", "&<", "&>", "-|-", "+", "-"} {
			q := "SELECT /*é*/ " + op + " a FROM op"
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+op+" int4range")
			d.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
			d.Position = "14"
			operatorDiagnostic(t, p, b, q, d, "prepare")
		}
		q := "SELECT /*é*/ a OPERATOR(public.@>) a FROM op"
		operatorDiagnostic(t, p, b, q, operatorMissing(q, "int4range", "public.@>", "int4range"), "prepare")
		q = "SELECT /*é*/ a OPERATOR(absent.@>) a FROM op"
		d := fixture.ControlsReferenceDiagnostic("3F000", "schema \"absent\" does not exist")
		d.Position = timestampRangeComparisonPosition(q, strings.Index(q, "OPERATOR("))
		operatorDiagnostic(t, p, b, q, d, "prepare")
		q = "SELECT /*é*/ missing @> a FROM op"
		d = fixture.ControlsReferenceDiagnostic("42703", "column \"missing\" does not exist")
		d.Position = "14"
		operatorDiagnostic(t, p, b, q, d, "prepare")
		// Selection contextualizes unknown as range, not the apparent element text.
		q = "SELECT a @> '2' FROM op"
		d = fixture.ControlsReferenceDiagnostic("22P02", "malformed range literal: \"2\"")
		d.Detail = "Missing left parenthesis or bracket."
		d.Position = timestampRangeComparisonPosition(q, strings.Index(q, "'2'"))
		operatorDiagnostic(t, p, b, q, d, "prepare")
		// Valid prepared analysis; parameter conversion fails only during execution.
		q = "SELECT a @> $1 FROM op"
		d.Position = ""
		d.Context = "unnamed portal parameter $1 = '...'"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d, "2")
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}

func TestReferenceRangeOperatorResolution(t *testing.T) {
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN span AS int4range", 0))
		run(fixture.Exec("CREATE DOMAIN chain AS span", 0))
		run(fixture.Exec("CREATE TABLE op(a int4range,d chain,m int4multirange,i integer,h smallint,l bigint,n numeric,dt date,ts timestamp,tz timestamptz,dr daterange,tr tsrange,zr tstzrange)", 0))
		run(fixture.Exec("INSERT INTO op VALUES ('[1,3)','[1,3)','{[1,3)}',2,2,2,2,'2000-01-02','2000-01-02','2000-01-02','[2000-01-01,2000-01-03)','[2000-01-01,2000-01-03)','[2000-01-01,2000-01-03)')", 1))
		for _, expr := range []string{"a @> '[1,2)'", "'[1,2)' <@ a", "m @> '{[1,2)}'", "'{[1,2)}' <@ m", "d @> '[1,2)'", "'[1,2)' <@ d", "a @> i", "dr @> dt", "tr @> ts", "zr @> tz"} {
			operatorBool(t, p, b, expr, "op", [][]any{{true}})
		}
		for _, expr := range []string{"a @> NULL", "NULL <@ a", "m @> NULL", "NULL <@ m"} {
			operatorBool(t, p, b, expr, "op", [][]any{{nil}})
		}
		for _, tc := range []struct{ expr, lt, op, rt string }{
			{"a @> h", "int4range", "@>", "smallint"}, {"a @> l", "int4range", "@>", "bigint"}, {"a @> n", "int4range", "@>", "numeric"},
			{"dr @> ts", "daterange", "@>", "timestamp without time zone"}, {"tr @> dt", "tsrange", "@>", "date"}, {"zr @> ts", "tstzrange", "@>", "timestamp without time zone"},
		} {
			q := "SELECT " + tc.expr + " FROM op"
			operatorDiagnostic(t, p, b, q, operatorMissing(q, tc.lt, tc.op, tc.rt), "prepare")
		}
		for _, op := range []string{"@>", "<@", "&&", "<<", ">>", "&<", "&>", "-|-", "+", "*", "-"} {
			q := "SELECT '[1,2)' " + op + " '[1,2)' FROM op"
			d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown "+op+" unknown")
			d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
			d.Position = timestampRangeComparisonPosition(q, strings.Index(q, " "+op+" ")+1)
			operatorDiagnostic(t, p, b, q, d, "prepare")
		}
		// Ordinary unknown comparisons resolve to TEXT, independently of range syntax.
		operatorBool(t, p, b, "'[1,2)' = '[1,2)'", "op", [][]any{{true}})
		operatorBool(t, p, b, "a @> $1", "op", [][]any{{true}}, "[1,2)")
		operatorBool(t, p, b, "$1 <@ a", "op", [][]any{{true}}, []byte("[1,2)"))
		operatorBool(t, p, b, "a @> $1 AND $1 <@ a", "op", [][]any{{true}}, "[1,2)")
		// Strict literal NULL must not evaluate a valid-but-failing range set child.
		operatorBool(t, p, b, "NULL @> (a + '[7,9)')", "op", [][]any{{nil}})
	})
}

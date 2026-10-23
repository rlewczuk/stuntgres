package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryRelationDiagnostics(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE geom_relation_errors(p point,s lseg,b box,l line)", 0))
		const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ left, op, right string }{
			{"point", "##", "point"}, {"lseg", "#", "line"}, {"box", "?#", "lseg"},
			{"lseg", "##", "line"}, {"point", "@>", "box"}, {"lseg", "&&", "lseg"},
		} {
			for _, prefix := range []string{"SELECT ", "SELECT /*é界*/ "} {
				for _, suffix := range []string{"", " FROM geom_relation_errors WHERE FALSE"} {
					q := prefix + "NULL::" + tc.left + " " + tc.op + " NULL::" + tc.right + suffix
					position := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, tc.op)]))
					for _, prepared := range []bool{false, true} {
						run(boxError(q, "42883", "operator does not exist: "+tc.left+" "+tc.op+" "+tc.right, position, hint, prepared, false))
					}
				}
			}
		}
		// ## has no preferred scalar candidate resolving two unknown operands.
		for _, q := range []string{"SELECT 'a' ## 'b'", "SELECT /*é界*/ 'a' ## 'b'"} {
			pos := fixture.Position(1 + utf8.RuneCountInString(q[:strings.Index(q, "##")]))
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "42725", "operator is not unique: unknown ## unknown", pos, "Could not choose a best candidate operator. You might need to add explicit type casts.", prepared, false))
			}
		}
		q := "SELECT $1::point ## $2::lseg AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			index int
			typ   string
			args  []any
		}{
			{1, "point", []any{"bad", grS}}, {2, "lseg", []any{grR, "bad"}},
		} {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+tc.typ+": \"bad\"")
			d.Context = "unnamed portal parameter $" + fixture.Position(tc.index) + " = '...'"
			s := fixture.ErrorDiagnostic(q, d, tc.args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"v"}, []string{"POINT"}, [][]any{{"(1,0)"}}, grR, grS))
		}
		// Representative shared spellings must retain their non-geometric
		// selection; exhaustive legacy coverage stays in its existing owners.
		s := fixture.Select("SELECT 7 # 3 AS xor,b'101' # b'011' AS bits,ARRAY[1,2] @> ARRAY[2] AS a,'{\"a\":1}'::jsonb @> '{\"a\":1}'::jsonb AS j,'a'::tsquery <-> 'b'::tsquery AS phrase", []string{"xor", "bits", "a", "j", "phrase"}, []string{"INT4", "BIT", "BOOL", "BOOL", "TSQUERY"}, [][]any{{int64(4), "110", true, true, "'a' <-> 'b'"}})
		run(s)
		run(fixture.Prepared(s))
	})
}

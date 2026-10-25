package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionBValues(t *testing.T) {
	geometryConversionBScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := func(t *testing.T, expr, typ string, want any) {
			t.Helper()
			s := geometryConversionSelect(b, "SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range geometryConversionBCases {
			t.Run(tc.name, func(t *testing.T) {
				check(t, tc.expression, tc.typ, tc.want)
				check(t, "pg_catalog."+tc.expression, tc.typ, tc.want)
				for mask := 1; mask < 1<<len(tc.formals); mask++ {
					check(t, geometryConversionBNullExpression(tc, mask), tc.typ, nil)
				}
			})
		}
		for _, tc := range []struct{ expr, typ, want string }{
			{"lseg(point '(1,0)',point '(-1,0)')", "LSEG", "[(1,0),(-1,0)]"},
			{"lseg(point '(2,3)',point '(2,3)')", "LSEG", "[(2,3),(2,3)]"},
			{"lseg(point '(-0,Infinity)',point '(NaN,5e-324)')", "LSEG", "[(-0,Infinity),(NaN,5e-324)]"},
			{"lseg(point '(0.10000000000000002,-0)',point '(1.0000000000000002,5e-324)')", "LSEG", "[(0.10000000000000002,-0),(1.0000000000000002,5e-324)]"},
			{"path(polygon '((2,3))')", "PATH", "((2,3))"},
			{"path(polygon '((0,0),(1,1))')", "PATH", "((0,0),(1,1))"},
			{"path(polygon '((0,0),(0,0),(6,0))')", "PATH", "((0,0),(0,0),(6,0))"},
			{"path(polygon '((0,0),(1,1),(0,0))')", "PATH", "((0,0),(1,1),(0,0))"},
			{"path(polygon '((-0,Infinity),(NaN,5e-324))')", "PATH", "((-0,Infinity),(NaN,5e-324))"},
			{"point(1,2)", "POINT", "(1,2)"},
			{"point('-0'::float8,'Infinity'::float8)", "POINT", "(-0,Infinity)"},
			{"point('NaN'::float8,5e-324::float8)", "POINT", "(NaN,5e-324)"},
			{"point(circle(point '(-0,2)',-1))", "POINT", "(-0,2)"},
			{"point(circle(point '(-0,2)','NaN'::float8))", "POINT", "(-0,2)"},
			{"point(circle(point '(-0,2)','Infinity'::float8))", "POINT", "(-0,2)"},
			{"point(box '(6,8),(0,0)')", "POINT", "(3,4)"},
			{"point(lseg '[(2,4),(8,10)]')", "POINT", "(5,7)"},
			{"point(lseg '[(8,10),(2,4)]')", "POINT", "(5,7)"},
			{"point(box '(-0,-0),(-0,-0)')", "POINT", "(-0,-0)"},
			{"point(lseg '[(-0,-0),(-0,-0)]')", "POINT", "(-0,-0)"},
			{"point(lseg '[(-0,0),(0,-0)]')", "POINT", "(0,0)"},
			{"point(box '(Infinity,Infinity),(-Infinity,-Infinity)')", "POINT", "(NaN,NaN)"},
			{"point(lseg '[(Infinity,Infinity),(-Infinity,-Infinity)]')", "POINT", "(NaN,NaN)"},
			{"point(polygon '((0,0),(0,0),(6,0))')", "POINT", "(2,0)"},
			{"point(polygon '((2,3))')", "POINT", "(2,3)"},
		} {
			t.Run(tc.expr, func(t *testing.T) { check(t, tc.expr, tc.typ, tc.want) })
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE geom_b_inverted(b box)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO geom_b_inverted VALUES(box '(4,4),(0,0)')", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE geom_b_inverted SET b[0]=point '(-2,-3)'", 1))
		s := geometryConversionSelect(b, "SELECT lseg(b) AS v FROM geom_b_inverted", []string{"v"}, []string{"LSEG"}, [][]any{{"[(-2,-3),(0,0)]"}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}

func TestReferenceGeometryConversionBBindingsMetadata(t *testing.T) {
	geometryConversionBScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range geometryConversionBCases {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.parameter + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				check := func(args []any, want any) {
					t.Helper()
					s := geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{want}}, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.args, tc.want)
				// Geometry importers accept typed-nil bytes as NULL. FLOAT8
				// retains the adapter's textual fallback, so use actual nil.
				nulls := []any{nil, []byte(nil)}
				for _, formal := range tc.formals {
					if formal == "float8" {
						nulls = nulls[:1]
						break
					}
				}
				for mask := 1; mask < 1<<len(tc.args); mask++ {
					for _, null := range nulls {
						args := append([]any(nil), tc.args...)
						for i := range args {
							if mask&(1<<i) != 0 {
								args[i] = null
							}
						}
						check(args, nil)
						check(tc.args, tc.want)
					}
				}
				args := append([]any(nil), tc.args...)
				for i, a := range args {
					if f, ok := a.(float64); ok {
						args[i] = []byte(strconv.FormatFloat(f, 'g', -1, 64))
					} else {
						args[i] = []byte(a.(string))
					}
				}
				check(args, tc.want)
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				s := geometryConversionSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
				s.CheckMetadata(t, rows)
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if nullable, known := cols[0].Nullable(); b.PG && (nullable || known) {
					t.Fatalf("pq nullability %t/%t", nullable, known)
				}
				for _, a := range args {
					for i := range a.([]byte) {
						a.([]byte)[i] = '?'
					}
				}
				if !rows.Next() {
					t.Fatalf("retained row: %v", rows.Err())
				}
				var value any
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				s.CheckRaw(t, 0, 0, value)
				if fixture.Normalize(value) != tc.want {
					t.Fatalf("retained value %v want %s", value, tc.want)
				}
				s.CheckMetadata(t, rows)
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("retained exhaustion %v", rows.Err())
				}
				rows.Close()
				check(tc.args, tc.want)
				empty := geometryConversionSelect(b, q+" WHERE false", []string{"v"}, []string{tc.typ}, nil, tc.args...)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, empty)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(empty))
				name := strings.SplitN(tc.expression, "(", 2)[0]
				unaliased := geometryConversionSelect(b, "SELECT "+tc.expression, []string{name}, []string{tc.typ}, [][]any{{tc.want}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, unaliased)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(unaliased))
				resultOID := int64(600)
				if tc.typ == "LSEG" {
					resultOID = 601
				}
				if tc.typ == "PATH" {
					resultOID = 602
				}
				// pg_typeof geometry overloads are not a prerequisite here.
				// Pair the fixed routine's exact result OID with the actual
				// populated, NULL and empty result DatabaseTypeName checks.
				s = fixture.Select(fmt.Sprintf("SELECT prorettype::int4 AS oid FROM pg_proc WHERE oid=%d", tc.oid), []string{"oid"}, []string{"INT4"}, [][]any{{resultOID}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
	})
}

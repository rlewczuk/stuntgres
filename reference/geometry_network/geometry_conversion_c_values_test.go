package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionCValues(t *testing.T) {
	geometryConversionCScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := func(t *testing.T, expr string, want any) {
			t.Helper()
			s := geometryConversionCSelect(b, "SELECT "+expr+" AS v", want)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range geometryConversionCCases {
			t.Run(tc.name, func(t *testing.T) {
				check(t, tc.expression, tc.want)
				check(t, "pg_catalog."+tc.expression, tc.want)
				for mask := 1; mask < 1<<len(tc.formals); mask++ {
					check(t, geometryConversionBNullExpression(tc, mask), nil)
				}
			})
		}
		for _, tc := range []struct{ expr, want string }{
			{"polygon(box '(0,0),(1,1)')", "((0,0),(0,1),(1,1),(1,0))"},
			{"polygon(box '(2,3),(2,3)')", "((2,3),(2,3),(2,3),(2,3))"},
			{"polygon(path '((2,3))')", "((2,3))"},
			{"polygon(path '((0,0),(1,1))')", "((0,0),(1,1))"},
			{"polygon(path '((0,0),(0,0),(6,0))')", "((0,0),(0,0),(6,0))"},
			{"polygon(path '((0,0),(1,1),(0,0))')", "((0,0),(1,1),(0,0))"},
			{"polygon(path '((-0,Infinity),(NaN,5e-324))')", "((-0,Infinity),(NaN,5e-324))"},
			{"polygon(4,circle(point '(2,3)',2))", geometryConversionCCircle(4, 2, 3, 2)},
			{"polygon(4,circle(point '(0,0)',-1))", geometryConversionCCircle(4, 0, 0, -1)},
			{"polygon(2,circle(point '(0,0)',-1.0000000000000002e-6))", geometryConversionCCircle(2, 0, 0, -1.0000000000000002e-6)},
			{"polygon(2,circle(point '(0,0)','NaN'::float8))", "((NaN,NaN),(NaN,NaN))"},
			{"polygon(2,circle(point '(0,0)','Infinity'::float8))", "((-Infinity,NaN),(Infinity,Infinity))"},
			{"polygon(2,circle(point '(0,0)','-Infinity'::float8))", "((Infinity,NaN),(-Infinity,-Infinity))"},
			{"polygon(0,NULL::circle)", ""},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				if tc.want == "" {
					check(t, tc.expr, nil)
				} else {
					check(t, tc.expr, tc.want)
				}
			})
		}
		for _, n := range []int{2, 3, 5, 12} {
			t.Run(fmt.Sprintf("count_%d", n), func(t *testing.T) {
				check(t, fmt.Sprintf("polygon(%d,circle '<(0,0),1>')", n), geometryConversionCCircle(n, 0, 0, 1))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE geom_c_inverted(b box)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO geom_c_inverted VALUES(box '(4,4),(0,0)')", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE geom_c_inverted SET b[0]=point '(-2,-3)'", 1))
		s := geometryConversionCSelect(b, "SELECT polygon(b) AS v FROM geom_c_inverted", "((0,0),(0,-3),(-2,-3),(-2,0))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}

func TestReferenceGeometryConversionCBindingsMetadata(t *testing.T) {
	geometryConversionCScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range geometryConversionCCases {
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
					s := geometryConversionCSelect(b, q, want, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.args, tc.want)
				for mask := 1; mask < 1<<len(tc.args); mask++ {
					args := append([]any(nil), tc.args...)
					for i := range args {
						if mask&(1<<i) != 0 {
							args[i] = nil
						}
					}
					check(args, nil)
					check(tc.args, tc.want)
				}
				args := append([]any(nil), tc.args...)
				geometryIndex := len(args) - 1
				args[geometryIndex] = []byte(nil)
				check(args, nil)
				args[geometryIndex] = []byte(tc.args[geometryIndex].(string))
				check(args, tc.want)
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				s := geometryConversionCSelect(b, q, tc.want)
				s.CheckMetadata(t, rows)
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if nullable, known := cols[0].Nullable(); b.PG && (nullable || known) {
					t.Fatalf("pq nullable %t/%t", nullable, known)
				}
				for i := range args[geometryIndex].([]byte) {
					args[geometryIndex].([]byte)[i] = '?'
				}
				if !rows.Next() {
					t.Fatalf("retained row: %v", rows.Err())
				}
				var v any
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				s.CheckRaw(t, 0, 0, v)
				if fixture.Normalize(v) != geometryConversionCExpected(b, tc.want) {
					t.Fatalf("owned row %v want %v", v, geometryConversionCExpected(b, tc.want))
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("exhaustion: %v", rows.Err())
				}
				rows.Close()
				check(tc.args, tc.want)
				empty := geometryConversionSelect(b, q+" WHERE false", []string{"v"}, []string{"POLYGON"}, nil, tc.args...)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, empty)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(empty))
				oid := fixture.Select(fmt.Sprintf("SELECT prorettype::int4 AS oid FROM pg_proc WHERE oid=%d", tc.oid), []string{"oid"}, []string{"INT4"}, [][]any{{int64(604)}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, oid)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(oid))
			})
		}
	})
}

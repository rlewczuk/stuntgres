package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGLSNComparison(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		values := []string{"0/0", "0/1", "0/FFFFFFFF", "1/0", "7FFFFFFF/FFFFFFFF", "80000000/0", "FFFFFFFF/FFFFFFFF"}
		run(fixture.Exec("CREATE TABLE pglsn_order(id integer,v pg_lsn)", 0))
		var asc, desc [][]any
		for i, v := range values {
			run(fixture.Exec("INSERT INTO pglsn_order VALUES($1,$2)", 1, i, v))
			asc = append(asc, []any{v})
			desc = append(desc, []any{values[len(values)-1-i]})
		}
		run(fixture.Exec("INSERT INTO pglsn_order VALUES(7,NULL)", 1))
		asc = append(asc, []any{nil})
		desc = append(desc, []any{nil})
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT v FROM pglsn_order ORDER BY v ASC NULLS LAST", []string{"v"}, []string{"PG_LSN"}, asc),
				fixture.Select("SELECT v FROM pglsn_order ORDER BY v DESC NULLS LAST", []string{"v"}, []string{"PG_LSN"}, desc),
				fixture.Select("SELECT id FROM pglsn_order WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}, "80000000/0"),
				fixture.Select("SELECT id FROM pglsn_order WHERE v=NULL::pg_lsn ORDER BY id", []string{"id"}, []string{"INT4"}, nil),
			} {
				s.Prepared = prepared
				run(s)
			}
			pairs := [][2]string{{values[0], values[len(values)-1]}, {values[len(values)-1], values[len(values)-1]}}
			for i := 0; i+1 < len(values); i++ {
				pairs = append(pairs, [2]string{values[i], values[i+1]})
			}
			for _, pair := range pairs {
				want := []any{false, true, true, true, false, false}
				if pair[0] == pair[1] {
					want = []any{true, false, false, true, false, true}
				}
				for i, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
					for _, q := range []string{"SELECT " + tidQuote(pair[0]) + "::pg_lsn " + op + " " + tidQuote(pair[1]) + "::pg_lsn AS v", "SELECT $1::pg_lsn " + op + " $2::pg_lsn AS v"} {
						s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want[i]}})
						if q[7] == '$' {
							s.Args = []any{pair[0], pair[1]}
						}
						s.Prepared = prepared
						run(s)
					}
					s := fixture.Select("SELECT NULL::pg_lsn "+op+" '0/0'::pg_lsn AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}})
					s.Prepared = prepared
					run(s)
				}
			}
		}
	})
}

func TestReferencePGLSNArithmetic(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ left, right, want string }{
				{"0/0", "0/0", "0"}, {"0/1", "0/0", "1"}, {"0/0", "0/1", "-1"},
				{"1/0", "0/0", "4294967296"}, {"0/0", "1/0", "-4294967296"},
				{"FFFFFFFF/FFFFFFFF", "0/0", "18446744073709551615"}, {"0/0", "FFFFFFFF/FFFFFFFF", "-18446744073709551615"},
			} {
				for _, param := range []bool{false, true} {
					q := "SELECT " + tidQuote(tc.left) + "::pg_lsn - " + tidQuote(tc.right) + "::pg_lsn AS d"
					var args []any
					if param {
						q = "SELECT $1::pg_lsn - $2::pg_lsn AS d"
						args = []any{tc.left, tc.right}
					}
					s := fixture.Select(q, []string{"d"}, []string{"NUMERIC"}, [][]any{{tc.want}}, args...)
					s.Prepared = prepared
					run(s)
				}
			}
			for _, tc := range []struct{ expression, want string }{
				{"'0/FFFFFFFF'::pg_lsn + 1", "1/0"}, {"1::numeric + '0/FFFFFFFF'::pg_lsn", "1/0"}, {"'1/0'::pg_lsn - 1", "0/FFFFFFFF"},
				{"'0/1'::pg_lsn + (-1)::numeric", "0/0"}, {"(-1)::numeric + '0/1'::pg_lsn", "0/0"}, {"'0/0'::pg_lsn - (-1)::numeric", "0/1"},
				{"'FFFFFFFF/FFFFFFFF'::pg_lsn + (-18446744073709551615)::numeric", "0/0"},
				{"'0/0'::pg_lsn + 18446744073709551615::numeric", "FFFFFFFF/FFFFFFFF"},
				{"'0/0'::pg_lsn + 0.49::numeric", "0/0"}, {"'0/0'::pg_lsn + 0.5::numeric", "0/1"},
				{"'0/1'::pg_lsn + (-0.5)::numeric", "0/1"}, {"'0/1'::pg_lsn - 0.5::numeric", "0/1"},
				{"'0/0'::pg_lsn - 0.49::numeric", "0/0"}, {"'FFFFFFFF/FFFFFFFF'::pg_lsn + 0.49::numeric", "FFFFFFFF/FFFFFFFF"},
			} {
				s := fixture.Select("SELECT "+tc.expression+" AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{tc.want}})
				s.Prepared = prepared
				run(s)
			}
			for _, expr := range []string{"NULL::pg_lsn + 'NaN'::numeric", "'NaN'::numeric + NULL::pg_lsn", "NULL::pg_lsn - 'NaN'::numeric", "NULL::pg_lsn + 'Infinity'::numeric", "'Infinity'::numeric + NULL::pg_lsn", "NULL::pg_lsn - 'Infinity'::numeric", "NULL::pg_lsn + '-Infinity'::numeric", "'-Infinity'::numeric + NULL::pg_lsn", "NULL::pg_lsn - '-Infinity'::numeric", "'0/1'::pg_lsn + NULL::numeric", "NULL::numeric + '0/1'::pg_lsn", "'0/1'::pg_lsn - NULL::numeric"} {
				s := fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{nil}})
				s.Prepared = prepared
				run(s)
			}
			s := fixture.Select("SELECT NULL::pg_lsn - '0/0'::pg_lsn AS d", []string{"d"}, []string{"NUMERIC"}, [][]any{{nil}})
			s.Prepared = prepared
			run(s)
			s = fixture.Select("SELECT '0/0'::pg_lsn - NULL::pg_lsn AS d", []string{"d"}, []string{"NUMERIC"}, [][]any{{nil}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("CREATE TABLE pglsn_arithmetic(id integer,v pg_lsn)", 0))
		run(fixture.Exec("INSERT INTO pglsn_arithmetic VALUES(1,'0/0'),(2,'FFFFFFFF/FFFFFFFF')", 2))
		run(fixture.Select("SELECT v-'0/0'::pg_lsn AS d FROM pglsn_arithmetic ORDER BY id", []string{"d"}, []string{"NUMERIC"}, [][]any{{"0"}, {"18446744073709551615"}}))
		q := "SELECT $1::pg_lsn + $2::numeric AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct{ lsn, offset, want string }{{"0/0", "0.5", "0/1"}, {"0/1", "-0.5", "0/1"}, {"0/FFFFFFFF", "1", "1/0"}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"PG_LSN"}, [][]any{{tc.want}}, tc.lsn, tc.offset))
		}
		rangeError := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22023", "pg_lsn out of range"), "FFFFFFFF/FFFFFFFF", "1")
		rangeError.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, rangeError)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"PG_LSN"}, [][]any{{"0/1"}}, "0/0", "1"))
	})
}

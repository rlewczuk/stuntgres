package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCircleOperators(t *testing.T) {
	cases := []struct {
		a, op, b string
		want     any
	}{
		{"0,0,1", "=", "9,9,2", false}, {"0,0,1", "<>", "9,9,2", true}, {"0,0,1", "<", "9,9,2", true}, {"0,0,1", ">", "9,9,2", false}, {"0,0,1", "<=", "9,9,2", true}, {"0,0,1", ">=", "9,9,2", false},
		{"9,9,2", "<", "0,0,1", false}, {"9,9,2", ">", "0,0,1", true}, {"9,9,2", "<=", "0,0,1", false}, {"9,9,2", ">=", "0,0,1", true},
		{"0,0,2", "=", "9,9,2", true}, {"0,0,2", "<>", "9,9,2", false}, {"0,0,2", "<", "9,9,2", false}, {"0,0,2", ">", "9,9,2", false}, {"0,0,2", "<=", "9,9,2", true}, {"0,0,2", ">=", "9,9,2", true},
		{"0,0,2", "~=", "9,9,2", false}, {"0,0,2", "~=", "0,0,2", true},
		{"0,0,1", "<<", "5,0,1", true}, {"5,0,1", ">>", "0,0,1", true}, {"0,0,1", "&<", "5,0,1", true}, {"5,0,1", "&>", "0,0,1", true},
		{"5,0,1", "<<", "0,0,1", false}, {"0,0,1", ">>", "5,0,1", false},
		{"0,0,1", "<<|", "0,5,1", true}, {"0,5,1", "|>>", "0,0,1", true}, {"0,0,1", "&<|", "0,5,1", true}, {"0,5,1", "|&>", "0,0,1", true},
		{"0,5,1", "<<|", "0,0,1", false}, {"0,0,1", "|>>", "0,5,1", false},
		{"0,0,1", "&<", "0,0,1", true}, {"0,0,1", "&>", "0,0,1", true}, {"0,0,1", "&<|", "0,0,1", true}, {"0,0,1", "|&>", "0,0,1", true}, {"0,0,1", "<<", "2,0,1", false},
		{"0,0,1", "&&", "2,0,1", true}, {"0,0,1", "&&", "3,0,1", false}, {"0,0,2", "@>", "1,0,1", true}, {"1,0,1", "<@", "0,0,2", true}, {"1,0,1", "@>", "0,0,2", false}, {"0,0,2", "<@", "1,0,1", false},
		{"0,0,1", "<->", "5,0,1", float64(3)}, {"0,0,1", "<->", "2,0,1", float64(0)}, {"0,0,2", "<->", "1,0,1", float64(0)},
		// Area differences ~6.28e-7 and ~1.26e-6 straddle EPSILON.
		{"0,0,1", "=", "9,9,1.0000001", true}, {"0,0,1", "<", "9,9,1.0000001", false}, {"0,0,1", "<>", "9,9,1.0000002", true}, {"0,0,1", "<", "9,9,1.0000002", true},
		{"0,0,1", "~=", "0.0000005,0,1.0000005", true}, {"0,0,1", "~=", "0.000002,0,1", false},
		{"NaN,0,NaN", "~=", "NaN,0,NaN", true},
	}
	for _, op := range []string{"=", "<>", "<", ">", "<=", ">="} {
		cases = append(cases, struct {
			a, op, b string
			want     any
		}{"0,0,NaN", op, "0,0,NaN", false})
	}
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, tc := range cases {
			t.Run(fmt.Sprintf("case_%d_%s", i, tc.op), func(t *testing.T) {
				typ := "BOOL"
				if tc.op == "<->" {
					typ = "FLOAT8"
				}
				for _, prepared := range []bool{false, true} {
					q := "SELECT $1::circle " + tc.op + " $2::circle AS result"
					s := fixture.Select(q, []string{"result"}, []string{typ}, [][]any{{tc.want}}, tc.a, tc.b)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				q := "SELECT " + circleLiteral(tc.a) + "::circle " + tc.op + " " + circleLiteral(tc.b) + " AS result"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"result"}, []string{typ}, [][]any{{tc.want}}))
			})
		}
		for _, op := range []string{"=", "<>", "<", ">", "<=", ">=", "~=", "<<", ">>", "&<", "&>", "<<|", "|>>", "&<|", "|&>", "@>", "<@", "&&", "<->"} {
			typ := "BOOL"
			if op == "<->" {
				typ = "FLOAT8"
			}
			for _, args := range [][]any{{nil, "0,0,1"}, {"0,0,1", nil}, {nil, nil}} {
				s := fixture.Select("SELECT $1::circle "+op+" $2::circle AS result", []string{"result"}, []string{typ}, [][]any{{nil}}, args...)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Select("SELECT '0,0,2'::circle OPERATOR(pg_catalog.=) '9,9,2'::circle AS area,pg_catalog.circle_same('0,0,2'::circle,'9,9,2'::circle) AS same", []string{"area", "same"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		run(fixture.Exec("CREATE TABLE circle_pred(id integer,v circle)", 0))
		run(fixture.Exec("INSERT INTO circle_pred VALUES (1,'0,0,2'),(2,'9,9,2'),(3,NULL),(4,'0,0,1')", 4))
		s := fixture.Select("SELECT id FROM circle_pred WHERE v='7,7,2' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Select("SELECT id FROM circle_pred WHERE v IN ('7,7,2'::circle) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT id FROM circle_pred WHERE v IN ('7,7,2'::circle,'0,0,3'::circle) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT id FROM circle_pred WHERE v = ANY(SELECT v FROM circle_pred WHERE id=2) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT a.id AS a,b.id AS b FROM circle_pred a JOIN circle_pred b ON a.v=b.v WHERE a.id=1 ORDER BY b.id", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}}))
		// Project ANY results so UNKNOWN cannot be mistaken for a filtered-out
		// false. Areas pi < 4*pi give true; NaN <> NaN is itself false.
		// With no true comparison, a NULL candidate makes ANY unknown; a true
		// candidate wins even when another candidate is NULL.
		for _, tc := range []struct {
			query string
			want  any
		}{
			{"SELECT '0,0,1'::circle < ANY(SELECT v FROM circle_pred WHERE id=2) AS result", true},
			{"SELECT '0,0,NaN'::circle <> ANY(SELECT '9,9,NaN'::circle) AS result", false},
			{"SELECT NULL::circle < ANY(SELECT v FROM circle_pred WHERE id=2) AS result", nil},
			{"SELECT '0,0,3'::circle < ANY(SELECT v FROM circle_pred WHERE id IN (2,3)) AS result", nil},
			{"SELECT '0,0,1'::circle < ANY(SELECT v FROM circle_pred WHERE id IN (2,3)) AS result", true},
		} {
			s := fixture.Select(tc.query, []string{"result"}, []string{"BOOL"}, [][]any{{tc.want}})
			run(s)
			run(fixture.Prepared(s))
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT $1='9,9,2'::circle AS area,$1!='9,9,2'::circle AS different", []string{"area", "different"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, "0,0,2")
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		run(fixture.Exec("CREATE DOMAIN circle_domain AS circle", 0))
		run(fixture.Select("SELECT '0,0,2'::circle_domain='9,9,2'::circle AS area,radius('1,2,3'::circle_domain) AS r", []string{"area", "r"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(3)}}))
		run(fixture.Exec("CREATE SCHEMA circle_shadow", 0))
		run(fixture.Exec("CREATE FUNCTION circle_shadow.circle_equal(circle,circle) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
		run(fixture.Exec("CREATE OPERATOR circle_shadow.= (LEFTARG=circle,RIGHTARG=circle,FUNCTION=circle_shadow.circle_equal)", 0))
		run(fixture.Select("SELECT '0,0,2'::circle OPERATOR(circle_shadow.=) '9,9,2'::circle AS custom,'0,0,2'::circle OPERATOR(pg_catalog.=) '9,9,2'::circle AS builtin", []string{"custom", "builtin"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		run(fixture.Exec("SET search_path=circle_shadow,pg_catalog,public", 0))
		run(fixture.Select("SELECT '0,0,2'::circle='9,9,2'::circle AS custom", []string{"custom"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("SET search_path=public", 0))
		for _, tc := range []struct{ a, op, b, want string }{
			{"Infinity,0,1", "<->", "0,0,1", "Infinity"},
			{"NaN,0,1", "<->", "0,0,1", "NaN"},
			{"Infinity,0,1", "<->", "Infinity,0,1", "NaN"},
			{"1e308,0,1", "<->", "0,0,1", "1e+308"},
		} {
			s := fixture.Select("SELECT ($1::circle "+tc.op+" $2::circle)::text AS distance", []string{"distance"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.a, tc.b)
			run(s)
			run(fixture.Prepared(s))
		}
		for _, tc := range []struct{ a, op, b, message string }{
			{"0,0,1e200", "=", "0,0,1", "value out of range: overflow"},
			{"0,0,1e-200", "<", "0,0,1", "value out of range: underflow"},
			{"1e308,0,1", "<->", "-1e308,0,1", "value out of range: overflow"},
			{"0,0,1e308", "&&", "0,0,1e308", "value out of range: overflow"},
		} {
			s := fixture.ErrorDiagnostic("SELECT $1::circle "+tc.op+" $2::circle", circleDiagnostic("22003", tc.message), tc.a, tc.b)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
		}
	})
}

package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var pointPredicates = []struct {
	op, routine string
	oid, proc   int64
}{
	{"~=", "point_eq", 510, 135}, {"<>", "point_ne", 713, 988},
	{"<<", "point_left", 507, 132}, {">>", "point_right", 508, 133},
	{"<<|", "point_below", 4162, 134}, {"|>>", "point_above", 4161, 131},
	{"?-", "point_horiz", 808, 990}, {"?|", "point_vert", 809, 989},
	{"!=", "point_ne", 713, 988}, {"<^", "point_below", 509, 134}, {">^", "point_above", 506, 131},
}

func TestReferencePointComparisons(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for index, pred := range pointPredicates {
			t.Run(pred.op, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "SELECT $1::point " + pred.op + " $2::point AS v"
				var held fixture.HeldStatement
				if pred.op == "~=" {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				// Columns follow the literal operator list above, including aliases.
				for _, tc := range []struct {
					a, z string
					want []any
				}{
					{"0,0", "0.0000005,0", []any{true, false, false, false, false, false, true, true, false, false, false}},
					{"0,0", "0.000001,0.000001", []any{true, false, false, false, false, false, true, true, false, false, false}},
					{"0,0", "0.000002,0.000002", []any{false, true, true, false, true, false, false, false, true, true, false}},
					{"0.000002,0.000002", "0,0", []any{false, true, false, true, false, true, false, false, true, false, true}},
					{"1,2", "9,2.0000005", []any{false, true, true, false, false, false, true, false, true, false, false}},
					{"1,2", "1.0000005,9", []any{false, true, false, false, true, false, false, true, true, true, false}},
					{"NaN,1", "NaN,1", []any{true, false, false, false, false, false, true, false, false, false, false}},
					{"NaN,1", "NaN,1.0000005", []any{false, true, false, false, false, false, true, false, true, false, false}},
					{"NaN,1", "0,1", []any{false, true, false, false, false, false, true, false, true, false, false}},
					{"NaN,0", "0,2", []any{false, true, false, false, true, false, false, false, true, true, false}},
					{"Infinity,0", "Infinity,-0", []any{true, false, false, false, false, false, true, true, false, false, false}},
				} {
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want[index]}}, tc.a, tc.z)
					run(s)
					run(fixture.Prepared(s))
					if held.Statement != nil {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					literal := fixture.Select("SELECT point "+boxQuote(tc.a)+" "+pred.op+" "+boxQuote(tc.z)+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want[index]}})
					run(literal)
					if index < 8 && tc.a == "0,0" && tc.z == "0.000002,0.000002" {
						routine := fixture.Select("SELECT pg_catalog."+pred.routine+"($1::point,$2::point) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want[index]}}, tc.a, tc.z)
						run(routine)
						run(fixture.Prepared(routine))
						run(fixture.Select("SELECT pg_catalog."+pred.routine+"(NULL::point,point '0,0') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
					}
				}
				for _, args := range [][]any{{nil, "0,0"}, {"0,0", nil}, {nil, nil}} {
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, args...)
					run(s)
					run(fixture.Prepared(s))
					if held.Statement != nil {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
				catalogOp := pred.op
				if catalogOp == "!=" {
					catalogOp = "<>"
				}
				run(fixture.Select("SELECT oid::bigint,oprleft::bigint,oprright::bigint,oprresult::bigint,oprcode::oid::bigint FROM pg_operator WHERE oprnamespace='pg_catalog'::regnamespace AND oprname="+boxQuote(catalogOp)+" AND oprleft=600 AND oprright=600", []string{"oid", "oprleft", "oprright", "oprresult", "oprcode"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{pred.oid, int64(600), int64(600), int64(16), pred.proc}}))
				run(fixture.Select("SELECT oid::bigint,proname,pronargs,prorettype::bigint,proisstrict FROM pg_proc WHERE oid=$1::oid", []string{"oid", "proname", "pronargs", "prorettype", "proisstrict"}, []string{"INT8", "NAME", "INT2", "INT8", "BOOL"}, [][]any{{pred.proc, pred.routine, int64(2), int64(16), true}}, pred.proc))
			})
		}
		// Ordinary inequality retains the existing scalar-subquery consumers.
		for _, tc := range []struct {
			q    string
			want any
		}{
			{"SELECT point '1,2' <> ANY(SELECT point '1,2') AS v", false},
			{"SELECT point '1,2' <> ANY(SELECT point '3,4') AS v", true},
			{"SELECT point '1,2' <> ALL(SELECT NULL::point) AS v", nil},
		} {
			s := fixture.Select(tc.q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
}

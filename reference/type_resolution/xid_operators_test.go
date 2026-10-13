package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func xidReferenceAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	s.SelectRows = true
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	s = fixture.Prepared(s)
	s.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func xidReferenceOperatorError(q, left, op, right string, pos int) fixture.Step {
	return fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: " + left + " " + op + " " + right, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(pos)})
}

func TestReferenceXIDOperators(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"'4294967295'::xid = '-1'::xid", true}, {"'0'::xid <> '1'::xid", true}, {"'0'::xid != '0'::xid", false},
			{"'4294967295'::xid = (-1)::int4", true}, {"'4294967295'::xid <> (-1)::int4", false},
			{"'0'::cid = '0'::cid", true}, {"'0'::cid = '1'::cid", false},
			{"'0'::xid8 < '9223372036854775808'::xid8", true}, {"'18446744073709551615'::xid8 > '9223372036854775808'::xid8", true},
			{"'18446744073709551615'::xid8 = '-1'::xid8", true}, {"'0'::xid8 <> '1'::xid8", true}, {"'0'::xid8 <= '0'::xid8", true}, {"'0'::xid8 >= '1'::xid8", false},
			{"NULL::xid = '0'::xid", nil}, {"NULL::xid <> 1::int4", nil}, {"NULL::cid = NULL::cid", nil}, {"NULL::xid8 >= '0'::xid8", nil},
			{"'4294967295'::xid = (-1)::int2", true},
		} {
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}))
		}
		for _, typ := range []string{"xid", "cid", "xid8"} {
			for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
				admitted := typ == "xid8" || typ == "xid" && (op == "=" || op == "<>" || op == "!=") || typ == "cid" && op == "="
				if admitted {
					continue
				}
				for _, value := range []string{"'0'", "NULL"} {
					q := "SELECT " + value + "::" + typ + " " + op + " " + value + "::" + typ
					actualOp := op
					if op == "!=" {
						actualOp = "<>"
					}
					xidReferenceAnalysisError(t, p, b, xidReferenceOperatorError(q, typ, actualOp, typ, strings.Index(q, " "+op+" ")+2))
				}
			}
		}
		for _, pair := range [][2]string{{"integer", "xid"}, {"bigint", "xid"}, {"xid", "bigint"}, {"xid", "cid"}, {"cid", "xid"}, {"xid", "xid8"}, {"xid8", "xid"}, {"cid", "xid8"}, {"xid8", "cid"}, {"xid", "oid"}, {"oid", "xid"}, {"cid", "oid"}, {"oid", "cid"}, {"xid8", "oid"}, {"oid", "xid8"}} {
			q := "SELECT NULL::" + pair[0] + " = NULL::" + pair[1]
			xidReferenceAnalysisError(t, p, b, xidReferenceOperatorError(q, pair[0], "=", pair[1], strings.Index(q, " = ")+2))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_order(id INT,x XID,c CID,y XID8)", 0))
		for _, phase := range []bool{false, true} {
			if phase {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO xid_order VALUES(1,NULL,NULL,'0'),(2,NULL,NULL,'9223372036854775808'),(3,NULL,NULL,'18446744073709551615'),(4,NULL,NULL,NULL)", 4))
			}
			for _, tc := range []struct{ col, typ string }{{"x", "xid"}, {"c", "cid"}} {
				q := "SELECT " + tc.col + " FROM xid_order ORDER BY " + tc.col
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "could not identify an ordering operator for type " + tc.typ, Hint: "Use an explicit ordering operator or modify the query.", Position: strconv.Itoa(strings.LastIndex(q, tc.col) + 1)})
				xidReferenceAnalysisError(t, p, b, s)
			}
		}
		xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT y FROM xid_order ORDER BY y", []string{"y"}, []string{"XID8"}, [][]any{{"0"}, {"9223372036854775808"}, {"18446744073709551615"}, {nil}}))
		xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT y FROM xid_order ORDER BY y DESC", []string{"y"}, []string{"XID8"}, [][]any{{nil}, {"18446744073709551615"}, {"9223372036854775808"}, {"0"}}))
	})
}

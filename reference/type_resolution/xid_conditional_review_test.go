package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXIDConditionalEquality(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, typ := range []string{"xid", "cid", "xid8"} {
			max := "4294967295"
			if typ == "xid8" {
				max = "18446744073709551615"
			}
			for _, tc := range []struct {
				expression string
				want       any
			}{
				{"NULLIF('" + max + "'::" + typ + ",'" + max + "'::" + typ + ")", nil},
				{"NULLIF('" + max + "'::" + typ + ",'0'::" + typ + ")", max},
				{"NULLIF('" + max + "'::" + typ + ",NULL::" + typ + ")", max},
				{"NULLIF(NULL::" + typ + ",'" + max + "'::" + typ + ")", nil},
				{"NULLIF('" + max + "'::" + typ + ",'" + max + "')", nil},
				{"NULLIF('" + max + "','0'::" + typ + ")", max},
			} {
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT "+tc.expression+" AS v", []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{tc.want}}))
			}
			for _, tc := range []struct {
				expression string
				want       bool
			}{
				{"CASE '" + max + "'::" + typ + " WHEN '" + max + "'::" + typ + " THEN TRUE ELSE FALSE END", true},
				{"CASE '" + max + "'::" + typ + " WHEN '0'::" + typ + " THEN TRUE ELSE FALSE END", false},
				{"CASE '" + max + "'::" + typ + " WHEN '" + max + "' THEN TRUE ELSE FALSE END", true},
				{"CASE NULL::" + typ + " WHEN NULL::" + typ + " THEN TRUE ELSE FALSE END", false},
			} {
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT "+tc.expression+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}))
			}
			table := "xid_review_cond_" + typ
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(id INT,v "+typ+")", 0))
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT NULLIF(v,v) AS v FROM "+table, []string{"v"}, []string{strings.ToUpper(typ)}, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES(1,NULL),(2,'"+max+"')", 2))
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT NULLIF(v,'0'::"+typ+") AS v FROM "+table+" ORDER BY id", []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{nil}, {max}}))
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT CASE v WHEN '"+max+"'::"+typ+" THEN TRUE ELSE FALSE END AS v FROM "+table+" ORDER BY id", []string{"v"}, []string{"BOOL"}, [][]any{{false}, {true}}))
			query := "SELECT NULLIF($1::" + typ + ",$2::" + typ + ") AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			for _, tc := range []struct{ left, right, want any }{{max, max, nil}, {max, "0", max}, {nil, max, nil}, {max, nil, max}} {
				xidReferenceRows(t, p.Ctx, b, held, fixture.Select(query, []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{tc.want}}, tc.left, tc.right))
			}
		}
		for _, right := range []string{"int4", "int2"} {
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT NULLIF('4294967295'::xid,(-1)::"+right+") AS v", []string{"v"}, []string{"XID"}, [][]any{{nil}}))
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT CASE '4294967295'::xid WHEN (-1)::"+right+" THEN TRUE ELSE FALSE END AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		}
		for _, pair := range [][2]string{{"integer", "xid"}, {"xid", "cid"}, {"cid", "xid"}, {"xid", "xid8"}, {"xid8", "xid"}, {"cid", "xid8"}, {"xid8", "cid"}, {"xid", "oid"}, {"oid", "xid"}} {
			for _, value := range []string{"'1'", "NULL"} {
				q := "SELECT NULLIF(" + value + "::" + pair[0] + "," + value + "::" + pair[1] + ")"
				xidReferenceAnalysisError(t, p, b, xidReferenceOperatorError(q, pair[0], "=", pair[1], 8))
				q = "SELECT CASE " + value + "::" + pair[0] + " WHEN " + value + "::" + pair[1] + " THEN TRUE ELSE FALSE END"
				xidReferenceAnalysisError(t, p, b, xidReferenceOperatorError(q, pair[0], "=", pair[1], strings.Index(q, "WHEN")+1))
			}
		}
	})
}

func TestReferenceXIDExtremaOrdering(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, typ := range []string{"xid", "cid"} {
			for _, function := range []string{"GREATEST", "LEAST"} {
				for _, args := range []string{"'1'::" + typ + ",'2'::" + typ, "NULL::" + typ + ",NULL::" + typ, "NULL::" + typ + ",'1'::" + typ} {
					q := "SELECT " + function + "(" + args + ")"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type " + typ})
					s.SelectRows = true
					s.ErrorStage = "execute"
					xidReferenceRoutes(t, p.Ctx, b, s)
				}
				table := "xid_review_extrema_" + typ + "_" + strings.ToLower(function)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(v "+typ+")", 0))
				for _, populated := range []bool{false, true} {
					if populated {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES(NULL)", 1))
					}
					s := fixture.ImplicitError("SELECT "+function+"(v,v) FROM "+table, fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type " + typ})
					s.SelectRows = true
					s.ErrorStage = "execute"
					xidReferenceRoutes(t, p.Ctx, b, s)
				}
			}
		}
		for _, tc := range []struct {
			expression string
			want       any
		}{
			{"GREATEST('0'::xid8,'9223372036854775808'::xid8,'18446744073709551615'::xid8)", "18446744073709551615"},
			{"LEAST('18446744073709551615'::xid8,'9223372036854775808'::xid8,'0'::xid8)", "0"},
			{"GREATEST(NULL::xid8,'9223372036854775808'::xid8)", "9223372036854775808"},
			{"LEAST(NULL::xid8,NULL::xid8)", nil},
		} {
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT "+tc.expression+" AS v", []string{"v"}, []string{"XID8"}, [][]any{{tc.want}}))
		}
		query := "SELECT GREATEST($1::xid8,$2::xid8) AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		for _, tc := range []struct{ left, right, want any }{{"9223372036854775808", "18446744073709551615", "18446744073709551615"}, {nil, "0", "0"}, {nil, nil, nil}} {
			xidReferenceRows(t, p.Ctx, b, held, fixture.Select(query, []string{"v"}, []string{"XID8"}, [][]any{{tc.want}}, tc.left, tc.right))
		}
	})
}

package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXIDCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, typ := range []string{"xid", "cid", "xid8"} {
			max := "4294967295"
			if typ == "xid8" {
				max = "18446744073709551615"
			}
			for _, value := range []string{"'" + max + "'", "NULL"} {
				var want any = max
				if value == "NULL" {
					want = nil
				}
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT ("+value+"::"+typ+")::"+typ+" AS v", []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{want}}))
			}
			for _, str := range []string{"text", "varchar", "bpchar"} {
				for _, expr := range []string{"CAST('" + max + "'::" + str + " AS " + typ + ")", "('" + max + "'::" + str + ")::" + typ, typ + "('" + max + "'::" + str + ")"} {
					xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{strings.ToUpper(typ)}, [][]any{{max}}))
				}
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT ('"+max+"'::"+typ+")::"+str+" AS v", []string{"v"}, []string{strings.ToUpper(str)}, [][]any{{max}}))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_io_"+typ+"_"+str+"(v "+str+")", 0))
				xidReferenceRoutes(t, p.Ctx, b, fixture.Exec("INSERT INTO xid_io_"+typ+"_"+str+" VALUES('"+max+"'::"+typ+")", 1))
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT v FROM xid_io_"+typ+"_"+str, []string{"v"}, []string{strings.ToUpper(str)}, [][]any{{max}, {max}}))
			}
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT ('"+max+"'::"+typ+")::varchar(3) AS v", []string{"v"}, []string{"VARCHAR"}, [][]any{{max[:3]}}))
			xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT ('1'::"+typ+")::char(3) AS v", []string{"v"}, []string{"BPCHAR"}, [][]any{{"1  "}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_io_target_"+typ+"(v "+typ+")", 0))
			for _, str := range []string{"text", "varchar", "bpchar"} {
				sqlName := map[string]string{"text": "text", "varchar": "character varying", "bpchar": "character"}[str]
				q := "INSERT INTO xid_io_target_" + typ + " VALUES('1'::" + str + ")"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: `column "v" is of type ` + typ + " but expression is of type " + sqlName, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(q, "'1'") + 1)})
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			q := "SELECT " + typ + "(1)"
			xidReferenceAnalysisError(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function " + typ + "(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}))
		}
		for _, tc := range []struct{ in, out string }{{"4294967296", "0"}, {"4294967297", "1"}, {"18446744073709551615", "4294967295"}, {"NULL", ""}} {
			expr := "'" + tc.in + "'"
			var want any = tc.out
			if tc.in == "NULL" {
				expr = "NULL"
				want = nil
			}
			for _, cast := range []string{"(" + expr + "::xid8)::xid", "CAST(" + expr + "::xid8 AS xid)"} {
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT "+cast+" AS v", []string{"v"}, []string{"XID"}, [][]any{{want}}))
			}
		}
		types := []string{"xid", "cid", "xid8", "oid", "smallint", "integer", "bigint"}
		for _, src := range types {
			for _, dst := range types {
				identifier := func(s string) bool { return s == "xid" || s == "cid" || s == "xid8" }
				if src == dst || src == "xid8" && dst == "xid" || !identifier(src) && !identifier(dst) {
					continue
				}
				for _, value := range []string{"'1'", "NULL"} {
					for _, syntax := range []bool{false, true} {
						q := "SELECT (" + value + "::" + src + ")::" + dst
						pos := strings.LastIndex(q, "::") + 1
						if syntax {
							q = "SELECT CAST(" + value + "::" + src + " AS " + dst + ")"
							pos = 8
						}
						xidReferenceAnalysisError(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type " + src + " to " + dst, Position: strconv.Itoa(pos)}))
					}
				}
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_cast_empty(v XID)", 0))
		q := "SELECT v::integer FROM xid_cast_empty"
		xidReferenceAnalysisError(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type xid to integer", Position: "9"}))
		q = "SELECT 1::xid"
		xidReferenceAnalysisError(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type integer to xid", Position: "9"}))
	})
}

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionRawSentinelExpressions(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
		for _, prepared := range []bool{false, true} {
			suffix := "direct"
			if prepared {
				suffix = "prepared"
			}
			for _, tc := range []struct{ name, lower, upper string }{
				{"mixedcase", "mInVaLuE", "mAxVaLuE"},
				{"quotedlower", `"minvalue"`, `"maxvalue"`},
				{"parenthesized", "((MINVALUE))", "((MAXVALUE))"},
			} {
				root := "rp_raw_" + tc.name + "_" + suffix
				leaf := root + "_leaf"
				step := fixture.Exec("CREATE TABLE "+root+"(k INT) PARTITION BY RANGE(k)", 0)
				step.Prepared = prepared
				run(step)
				step = fixture.Exec("CREATE TABLE "+leaf+" PARTITION OF "+root+" FOR VALUES FROM("+tc.lower+") TO("+tc.upper+")", 0)
				step.Prepared = prepared
				run(step)
				step = fixture.Exec("INSERT INTO "+root+" VALUES(-1),(1)", 2)
				step.Prepared = prepared
				run(step)
				step = fixture.Select("SELECT k FROM "+root+" ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}})
				step.Prepared = prepared
				run(step)
			}
			root := "rp_raw_error_" + suffix
			run(fixture.Exec("CREATE TABLE "+root+"(k INT) PARTITION BY RANGE(k)", 0))
			for _, tc := range []struct{ name, expression, token string }{
				{"quotedupper", `"MINVALUE"`, `"MINVALUE"`},
				{"quotedmixed", `"MinValue"`, `"MinValue"`},
				{"qualified", root + ".k", root + ".k"},
				{"cast", "MINVALUE::INT", "MINVALUE"},
				{"operator", "MINVALUE+0", "MINVALUE"},
			} {
				leaf := "rp_raw_bad_" + tc.name + "_" + suffix
				query := "CREATE TABLE " + leaf + " PARTITION OF " + root + " FOR VALUES FROM(" + tc.expression + ") TO(10)"
				step := referenceRangeError(query, "0A000", "cannot use column reference in partition bound expression", fixture.Diagnostic{Position: referenceRangePosition(query, tc.token)})
				step.Prepared = prepared
				run(step)
				run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='"+leaf+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			run(fixture.Select("SELECT COUNT(*) FROM "+root, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			textRoot := "rp_raw_text_" + suffix
			step := fixture.Exec("CREATE TABLE "+textRoot+"(k TEXT) PARTITION BY RANGE(k)", 0)
			step.Prepared = prepared
			run(step)
			step = fixture.Exec("CREATE TABLE "+textRoot+"_leaf PARTITION OF "+textRoot+" FOR VALUES FROM('maxvalue'::TEXT) TO('minvalue'::TEXT)", 0)
			step.Prepared = prepared
			run(step)
			step = fixture.Exec("INSERT INTO "+textRoot+" VALUES('maxvalue'),('maxvaluez')", 2)
			step.Prepared = prepared
			run(step)
			step = fixture.Select("SELECT k FROM "+textRoot+" ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"maxvalue"}, {"maxvaluez"}})
			step.Prepared = prepared
			run(step)
		}
	})
}

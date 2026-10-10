package reference_test

import "testing"

func TestReferenceRangePartitionRawSentinelExpressions(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(step referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, step) }
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
				step := referenceExec("CREATE TABLE "+root+"(k INT) PARTITION BY RANGE(k)", 0)
				step.prepared = prepared
				run(step)
				step = referenceExec("CREATE TABLE "+leaf+" PARTITION OF "+root+" FOR VALUES FROM("+tc.lower+") TO("+tc.upper+")", 0)
				step.prepared = prepared
				run(step)
				step = referenceExec("INSERT INTO "+root+" VALUES(-1),(1)", 2)
				step.prepared = prepared
				run(step)
				step = referenceSelect("SELECT k FROM "+root+" ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}})
				step.prepared = prepared
				run(step)
			}
			root := "rp_raw_error_" + suffix
			run(referenceExec("CREATE TABLE "+root+"(k INT) PARTITION BY RANGE(k)", 0))
			for _, tc := range []struct{ name, expression, token string }{
				{"quotedupper", `"MINVALUE"`, `"MINVALUE"`},
				{"quotedmixed", `"MinValue"`, `"MinValue"`},
				{"qualified", root + ".k", root + ".k"},
				{"cast", "MINVALUE::INT", "MINVALUE"},
				{"operator", "MINVALUE+0", "MINVALUE"},
			} {
				leaf := "rp_raw_bad_" + tc.name + "_" + suffix
				query := "CREATE TABLE " + leaf + " PARTITION OF " + root + " FOR VALUES FROM(" + tc.expression + ") TO(10)"
				step := referenceRangeError(query, "0A000", "cannot use column reference in partition bound expression", referenceDiagnostic{Position: referenceRangePosition(query, tc.token)})
				step.prepared = prepared
				run(step)
				run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='"+leaf+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			run(referenceSelect("SELECT COUNT(*) FROM "+root, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			textRoot := "rp_raw_text_" + suffix
			step := referenceExec("CREATE TABLE "+textRoot+"(k TEXT) PARTITION BY RANGE(k)", 0)
			step.prepared = prepared
			run(step)
			step = referenceExec("CREATE TABLE "+textRoot+"_leaf PARTITION OF "+textRoot+" FOR VALUES FROM('maxvalue'::TEXT) TO('minvalue'::TEXT)", 0)
			step.prepared = prepared
			run(step)
			step = referenceExec("INSERT INTO "+textRoot+" VALUES('maxvalue'),('maxvaluez')", 2)
			step.prepared = prepared
			run(step)
			step = referenceSelect("SELECT k FROM "+textRoot+" ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"maxvalue"}, {"maxvaluez"}})
			step.prepared = prepared
			run(step)
		}
	})
}

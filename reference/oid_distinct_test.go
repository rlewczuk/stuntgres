//go:build reference

package reference_test

import "testing"

func TestReferenceOIDMixedDistinct(t *testing.T) {
	q := "SELECT v IS DISTINCT FROM s,s IS DISTINCT FROM v,v IS NOT DISTINCT FROM s,s IS NOT DISTINCT FROM v,v IS DISTINCT FROM i,i IS DISTINCT FROM v,v IS NOT DISTINCT FROM i,i IS NOT DISTINCT FROM v,v IS DISTINCT FROM b,b IS DISTINCT FROM v,v IS NOT DISTINCT FROM b,b IS NOT DISTINCT FROM v FROM one ORDER BY id"
	columns, types := make([]string, 12), make([]string, 12)
	for i := range columns {
		columns[i], types[i] = "?column?", "BOOL"
	}
	selectStep := referenceSelect(q, columns, types, [][]any{
		{false, false, true, true, false, false, true, true, false, false, true, true},
		{false, false, true, true, false, false, true, true, false, false, true, true},
		{true, true, false, false, true, true, false, false, true, true, false, false},
		{true, true, false, false, true, true, false, false, true, true, false, false},
		{true, true, false, false, true, true, false, false, true, true, false, false},
	})
	steps := []referenceStep{
		referenceExec("CREATE TABLE one(id INT,s SMALLINT,i INT,b BIGINT,v OID)", 0),
		referenceExec("INSERT INTO one VALUES(1,-1,-1,4294967295,'4294967295'),(2,NULL,NULL,NULL,NULL),(3,-1,-1,4294967295,NULL),(4,1,1,1,'0'),(5,NULL,NULL,NULL,'4294967295')", 5),
		selectStep, referencePrepared(selectStep),
	}
	for _, value := range []string{"(-1)::int8", "4294967296::int8"} {
		for _, kind := range []string{" IS DISTINCT FROM ", " IS NOT DISTINCT FROM "} {
			for _, q := range []string{"SELECT v" + kind + value + " FROM one", "SELECT " + value + kind + "v FROM one"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "OID out of range"})
				s.errorStage = "execute"
				steps = append(steps, s, referencePrepared(s))
			}
		}
	}
	for _, tc := range []struct {
		value          any
		distinct, same bool
	}{{"4294967295", false, true}, {nil, true, false}, {"0", true, false}} {
		steps = append(steps, referencePrepared(referenceSelect("SELECT v IS DISTINCT FROM $1,$1 IS NOT DISTINCT FROM v FROM one WHERE id=1", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{tc.distinct, tc.same}}, tc.value)))
	}
	s := referenceSelect("SELECT NULL::oid IS DISTINCT FROM s,s IS NOT DISTINCT FROM NULL::oid,NULL::oid IS DISTINCT FROM i,i IS NOT DISTINCT FROM NULL::oid,NULL::oid IS DISTINCT FROM b,b IS NOT DISTINCT FROM NULL::oid,v=NULL::oid FROM one WHERE id=2", columns[:7], types[:7], [][]any{{false, true, false, true, false, true, nil}})
	steps = append(steps, s, referencePrepared(s))
	nameReferenceSteps(t, steps)
}

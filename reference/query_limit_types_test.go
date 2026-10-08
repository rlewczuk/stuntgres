//go:build reference

package reference_test

import "testing"

func TestReferenceQueryLimitTypingProvider(t *testing.T) {
	p:=referenceLimitFixture(t)
	for _,tc:=range []struct{bound string;ids []int64}{
		{"CAST(2 AS SMALLINT)",[]int64{1,2}},
		{"CAST(2 AS INTEGER)",[]int64{1,2}},
		{"CAST(2 AS BIGINT)",[]int64{1,2}},
		{"CAST(2 AS OID)",[]int64{1,2}},
		{"CAST(2.5 AS NUMERIC)",[]int64{1,2,3}},
		{"CAST(2.5 AS REAL)",[]int64{1,2}},
		{"CAST(2.5 AS DOUBLE PRECISION)",[]int64{1,2}},
		{"CAST(NULL AS NUMERIC)",[]int64{1,2,3,4}},
		{"CAST(NULL AS DOUBLE PRECISION)",[]int64{1,2,3,4}},
	} {
		t.Run(tc.bound,func(t *testing.T){
			step:=referenceSelect("SELECT id FROM t ORDER BY id LIMIT "+tc.bound,[]string{"id"},[]string{"INT4"},referenceLimitIDs(tc.ids...))
			p.step(t,step)
			p.step(t,referencePrepared(step))
		})
	}
	step:=referenceSelect("SELECT id FROM t ORDER BY id FETCH FIRST CAST(2 AS BIGINT) ROWS ONLY",[]string{"id"},[]string{"INT4"},referenceLimitIDs(1,2))
	p.step(t,step)
	p.step(t,referencePrepared(step))
	for _,tc:=range []struct{bound,code,message string}{
		{"CAST('NaN' AS NUMERIC)","0A000","cannot convert NaN to bigint"},
		{"CAST('Infinity' AS NUMERIC)","0A000","cannot convert infinity to bigint"},
		{"CAST('-Infinity' AS NUMERIC)","0A000","cannot convert infinity to bigint"},
		{"CAST('NaN' AS REAL)","22003","bigint out of range"},
		{"CAST('Infinity' AS DOUBLE PRECISION)","22003","bigint out of range"},
		{"CAST('-Infinity' AS DOUBLE PRECISION)","22003","bigint out of range"},
		{"9223372036854775808","22003","bigint out of range"},
		{"CAST('9223372036854775807' AS DOUBLE PRECISION)","22003","bigint out of range"},
	} {
		t.Run(tc.bound,func(t *testing.T){
			step:=referenceImplicitError("SELECT id FROM t LIMIT "+tc.bound,referenceDiagnostic{Code:tc.code,Message:tc.message})
			step.selectRows=true
			step.errorStage="execute"
			p.step(t,step)
			p.step(t,referencePrepared(step))
		})
	}
}

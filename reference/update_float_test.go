//go:build reference

package reference_test

import "testing"

func TestReferenceUpdateCoreFloatIntegerAssignment(t *testing.T) {
	p := newReferencePair(t)
	for _, source := range []string{"REAL", "DOUBLE PRECISION"} {
		for _, target := range []struct{ sql, metadata, name string }{{"SMALLINT", "INT2", "smallint"}, {"INT", "INT4", "integer"}, {"BIGINT", "INT8", "bigint"}} {
			p.step(t, referenceExec("CREATE TABLE t(id INT,f "+source+",n "+target.sql+")", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1,2.5,1),(2,-2.5,1),(3,3.5,1),(4,-3.5,1)", 4))
			want := [][]any{{int64(1), int64(2)}, {int64(2), int64(-2)}, {int64(3), int64(4)}, {int64(4), int64(-4)}}
			for _, prepared := range []bool{false, true} {
				step := referenceExec("UPDATE t SET n=f", 4)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT id,n FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", target.metadata}, want))
			}
			overflow := map[string]string{"SMALLINT": "32767.5", "INT": "2147483648", "BIGINT": "9223372036854775808"}[target.sql]
			underflow := map[string]string{"SMALLINT": "-32768.51", "INT": "-2147483648.51", "BIGINT": "-9223372036854777856"}[target.sql]
			if source == "REAL" {
				underflow = map[string]string{"SMALLINT": "-32768.51", "INT": "-2147483904", "BIGINT": "-9223373136366403584"}[target.sql]
			}
			for _, input := range []string{overflow, underflow, "NaN", "Infinity", "-Infinity"} {
				p.step(t, referenceExec("UPDATE t SET n=1", 4))
				p.step(t, referenceExec("UPDATE t SET f='"+input+"' WHERE id=4", 1))
				for _, prepared := range []bool{false, true} {
					step := referenceImplicitError("UPDATE t SET n=f", referenceDiagnostic{Code: "22003", Message: target.name + " out of range"})
					step.prepared = prepared
					step.errorStage = "execute"
					p.step(t, step)
					p.step(t, referenceSelect("SELECT n FROM t ORDER BY id", []string{"n"}, []string{target.metadata}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}))
				}
			}
			p.step(t, referenceExec("DROP TABLE t", 0))
		}
	}
	// These expected integers are independently specified IEEE neighbors, not
	// values computed by the native coercion/rounding implementation.
	for _, tc := range []struct {
		source, target, metadata, input string
		want                            int64
	}{
		{"DOUBLE PRECISION", "SMALLINT", "INT2", "32767.49", 32767},
		{"DOUBLE PRECISION", "SMALLINT", "INT2", "-32768.5", -32768},
		{"REAL", "INT", "INT4", "2147483520", 2147483520},
		{"DOUBLE PRECISION", "INT", "INT4", "2147483647.49", 2147483647},
		{"DOUBLE PRECISION", "INT", "INT4", "-2147483648.5", -2147483648},
		{"DOUBLE PRECISION", "BIGINT", "INT8", "9223372036854774784", 9223372036854774784},
		{"DOUBLE PRECISION", "BIGINT", "INT8", "-9223372036854775808", -9223372036854775808},
		{"REAL", "BIGINT", "INT8", "9223371487098961920", 9223371487098961920},
		{"REAL", "BIGINT", "INT8", "-9223372036854775808", -9223372036854775808},
	} {
		p.step(t, referenceExec("CREATE TABLE t(f "+tc.source+",n "+tc.target+")", 0))
		p.step(t, referenceExec("INSERT INTO t VALUES('"+tc.input+"',1)", 1))
		p.step(t, referenceExec("UPDATE t SET n=f", 1))
		p.step(t, referencePrepared(referenceExec("UPDATE t SET n=f", 1)))
		p.step(t, referenceSelect("SELECT n FROM t", []string{"n"}, []string{tc.metadata}, [][]any{{tc.want}}))
		p.step(t, referenceExec("DROP TABLE t", 0))
	}
	p.step(t, referenceExec("CREATE TABLE inserted(n INT)", 0))
	p.step(t, referenceExec("INSERT INTO inserted VALUES(2.5),(-2.5)", 2))
	p.step(t, referenceSelect("SELECT n FROM inserted ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(-3)}, {int64(3)}}))
}

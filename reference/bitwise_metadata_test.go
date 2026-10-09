package reference_test

import "testing"

func TestReferenceBitwiseComputedMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(s SMALLINT,i INTEGER,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(NULL,NULL,NULL)", 1))
	for _, query := range []string{"SELECT ~s,i & 1,b << 1 FROM t", "SELECT ~s,i & 1,b << 1 FROM t WHERE FALSE"} {
		want := [][]any{{nil, nil, nil}}
		if query == "SELECT ~s,i & 1,b << 1 FROM t WHERE FALSE" {
			want = nil
		}
		referenceArithmeticMetadata(t, p, referenceSelect(query, []string{"?column?", "?column?", "?column?"}, []string{"INT2", "INT4", "INT8"}, want))
	}
}

func TestReferenceBitwiseHeldParameterFrames(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(s SMALLINT,i INTEGER,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(NULL,NULL,NULL)", 1))
	const query = "SELECT s & $1,b << $2 FROM t"
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := referenceHeldStatement{backend.db, stmt, query}
			for _, tc := range []struct{ arg, code, message string }{
				{"bad", "22P02", `invalid input syntax for type smallint: "bad"`},
				{"32768", "22003", `value "32768" is out of range for type smallint`},
			} {
				step := referenceImplicitError(query, referenceDiagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, tc.arg, 1)
				step.selectRows, step.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, held, backend.recorder, step)
			}
			for _, arg := range []any{3, nil, 1} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(query, []string{"?column?", "?column?"}, []string{"INT2", "INT8"}, [][]any{{nil, nil}}, arg, 1))
			}
		})
	}
}

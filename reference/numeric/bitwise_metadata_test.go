package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitwiseComputedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(s SMALLINT,i INTEGER,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(NULL,NULL,NULL)", 1))
	for _, query := range []string{"SELECT ~s,i & 1,b << 1 FROM t", "SELECT ~s,i & 1,b << 1 FROM t WHERE FALSE"} {
		want := [][]any{{nil, nil, nil}}
		if query == "SELECT ~s,i & 1,b << 1 FROM t WHERE FALSE" {
			want = nil
		}
		referenceArithmeticMetadata(t, p, fixture.Select(query, []string{"?column?", "?column?", "?column?"}, []string{"INT2", "INT4", "INT8"}, want))
	}
}

func TestReferenceBitwiseHeldParameterFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(s SMALLINT,i INTEGER,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(NULL,NULL,NULL)", 1))
	const query = "SELECT s & $1,b << $2 FROM t"
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, tc := range []struct{ arg, code, message string }{
				{"bad", "22P02", `invalid input syntax for type smallint: "bad"`},
				{"32768", "22003", `value "32768" is out of range for type smallint`},
			} {
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, tc.arg, 1)
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
			}
			for _, arg := range []any{3, nil, 1} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"?column?", "?column?"}, []string{"INT2", "INT8"}, [][]any{{nil, nil}}, arg, 1))
			}
		})
	}
}

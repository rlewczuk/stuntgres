package reference_test

import (
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

type referenceByteaValuer struct {
	raw   driver.Value
	err   error
	calls *int
}

func (v referenceByteaValuer) Value() (driver.Value, error) { (*v.calls)++; return v.raw, v.err }

func TestReferenceByteaValuerAndInference(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0))
			for i, tc := range []struct {
				raw  driver.Value
				want []byte
			}{
				{`\x41`, []byte{92, 120, 52, 49}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}},
			} {
				for _, prepared := range []bool{false, true} {
					calls := 0
					s := fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, i, referenceByteaValuer{tc.raw, nil, &calls})
					s.Prepared = prepared
					referenceByteaExec(t, p.Ctx, b.DB, b, s)
					if calls != 1 {
						t.Fatal("Value calls", calls)
					}
				}
				referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t WHERE id=$1", false, []string{"v"}, []int{1}, [][]any{{tc.want}, {tc.want}}, false, i)
			}
			sentinel := errors.New("valuer failure")
			calls := 0
			_, err := b.DB.ExecContext(p.Ctx, "INSERT INTO t VALUES(9,$1)", referenceByteaValuer{nil, sentinel, &calls})
			if !errors.Is(err, sentinel) || calls != 1 {
				t.Fatal(err, calls)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary Valuer error became SQL error")
			}
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM t WHERE id=9", false, []string{"v"}, []int{1}, nil, false)
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT $1 FROM t WHERE v=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus text", Position: "8"}, []byte{65})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		})
	}
}

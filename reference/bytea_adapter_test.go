package reference_test

import (
	"database/sql/driver"
	"errors"
	"testing"
)

type referenceByteaValuer struct {
	raw   driver.Value
	err   error
	calls *int
}

func (v referenceByteaValuer) Value() (driver.Value, error) { (*v.calls)++; return v.raw, v.err }

func TestReferenceByteaValuerAndInference(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceByteaExec(t, p.ctx, b.db, b, referenceExec("CREATE TABLE t(id INTEGER,v BYTEA)", 0))
			for i, tc := range []struct {
				raw  driver.Value
				want []byte
			}{
				{`\x41`, []byte{92, 120, 52, 49}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}},
			} {
				for _, prepared := range []bool{false, true} {
					calls := 0
					s := referenceExec("INSERT INTO t VALUES($1,$2)", 1, i, referenceByteaValuer{tc.raw, nil, &calls})
					s.prepared = prepared
					referenceByteaExec(t, p.ctx, b.db, b, s)
					if calls != 1 {
						t.Fatal("Value calls", calls)
					}
				}
				referenceByteaRaw(t, p.ctx, b.db, b, "SELECT v FROM t WHERE id=$1", false, []string{"v"}, []int{1}, [][]any{{tc.want}, {tc.want}}, false, i)
			}
			sentinel := errors.New("valuer failure")
			calls := 0
			_, err := b.db.ExecContext(p.ctx, "INSERT INTO t VALUES(9,$1)", referenceByteaValuer{nil, sentinel, &calls})
			if !errors.Is(err, sentinel) || calls != 1 {
				t.Fatal(err, calls)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary Valuer error became SQL error")
			}
			referenceByteaRaw(t, p.ctx, b.db, b, "SELECT v FROM t WHERE id=9", false, []string{"v"}, []int{1}, nil, false)
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT $1 FROM t WHERE v=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus text", Position: "8"}, []byte{65})
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		})
	}
}

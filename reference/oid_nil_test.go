package reference_test

import (
	"context"
	"testing"
)

func TestReferenceOIDTypedNilParameters(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE o(id INT,v OID,w OID)", 0))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO o VALUES($1,$2,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 0, nil))
			want := [][]any{{int64(0), nil, nil}}
			if backend.pg {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 1, []byte(nil)))
				want = append(want, []any{int64(1), nil, nil})
				result, err := stmt.ExecContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), 2, []byte{})
				if result != nil {
					t.Fatal("failed empty OID exposed result")
				}
				referenceState(t, err, "22P02")
				failure := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: ""`, Context: "unnamed portal parameter $2 = ''"})
				referenceCheckDiagnostic(t, err, failure, "execute")
			} else {
				failure := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: ""`, Context: "unnamed portal parameter $2 = '...'"}, 1, []byte(nil))
				failure.errorStage = "execute"
				referenceRunStep(t, p.ctx, held, backend.recorder, failure)
				oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT * FROM o ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "OID", "OID"}, want))
			}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 3, "4294967295"))
			want = append(want, []any{int64(3), int64(4294967295), int64(4294967295)})
			oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT * FROM o ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "OID", "OID"}, want))
		}()
	}
}

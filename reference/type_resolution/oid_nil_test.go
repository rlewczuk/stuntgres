package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOIDTypedNilParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE o(id INT,v OID,w OID)", 0))
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO o VALUES($1,$2,$2)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 0, nil))
			want := [][]any{{int64(0), nil, nil}}
			if backend.PG {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 1, []byte(nil)))
				want = append(want, []any{int64(1), nil, nil})
				result, err := stmt.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), 2, []byte{})
				if result != nil {
					t.Fatal("failed empty OID exposed result")
				}
				fixture.State(t, err, "22P02")
				failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: ""`, Context: "unnamed portal parameter $2 = ''"})
				fixture.CheckDiagnostic(t, err, failure, "execute")
			} else {
				failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: ""`, Context: "unnamed portal parameter $2 = '...'"}, 1, []byte(nil))
				failure.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
				fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT * FROM o ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "OID", "OID"}, want))
			}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 3, "4294967295"))
			want = append(want, []any{int64(3), int64(4294967295), int64(4294967295)})
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT * FROM o ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "OID", "OID"}, want))
		}()
	}
}

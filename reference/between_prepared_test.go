package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceBetweenHeldPrepared(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE m(i INTEGER,s TEXT,j BIGINT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO m VALUES(1,'3',3)", 1))
	for _, form := range betweenReferenceForms {
		for _, backend := range p.backends() {
			db := backend.db
			stmt, err := db.PrepareContext(p.ctx, "SELECT $1 "+form+" i AND j FROM m")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, arg := range []any{2, nil, 2} {
				rows, err := stmt.QueryContext(p.ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "BOOL" {
					t.Fatal(meta, err)
				}
				scalarAssertMetadata(t, backend.pg, meta)
				if !rows.Next() {
					t.Fatal("missing prepared row", rows.Err())
				}
				var got any
				if err := rows.Scan(&got); err != nil {
					t.Fatal(err)
				}
				var want any = form == "BETWEEN" || form == "BETWEEN SYMMETRIC"
				if arg == nil {
					want = nil
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal(form, arg, got, want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra row/error", rows.Err())
				}
				rows.Close()
			}
			stmt.Close()
		}
	}
	for _, form := range betweenReferenceForms {
		for _, expr := range []string{"j " + form + " $1 AND j", "i " + form + " i AND $1"} {
			betweenReferenceBoth(t, p, referenceSelect("SELECT "+expr+" FROM m", []string{"?column?"}, []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}}, 3))
		}
	}
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ form, op string }{{"BETWEEN", "<="}, {"NOT BETWEEN", ">"}, {"BETWEEN SYMMETRIC", "<="}, {"NOT BETWEEN SYMMETRIC", ">"}} {
		step := referenceImplicitError("SELECT $1 "+tc.form+" i AND s FROM m", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer " + tc.op + " text", Position: "11", Hint: hint}, 2)
		step.selectRows = true
		step.errorStage = "execute"
		scalarStep(t, p, step)
		step.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(step))
	}
}

func TestReferenceBetweenSyntaxDiagnostics(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, tc := range []struct{ q, message, pos string }{
		{"SELECT v BETWEEN 1 3 FROM t", `syntax error at or near "3"`, "20"},
		{"SELECT v BETWEEN AND 3 FROM t", `syntax error at or near "AND"`, "18"},
		{"SELECT v BETWEEN 1 AND", `syntax error at end of input`, "23"},
	} {
		step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42601", Message: tc.message, Position: tc.pos})
		step.selectRows = true
		step.errorStage = "execute"
		scalarStep(t, p, step)
		step.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(step))
	}
}

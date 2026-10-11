package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBetweenHeldPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE m(i INTEGER,s TEXT,j BIGINT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO m VALUES(1,'3',3)", 1))
	for _, form := range betweenReferenceForms {
		for _, backend := range p.Backends() {
			db := backend.DB
			stmt, err := db.PrepareContext(p.Ctx, "SELECT $1 "+form+" i AND j FROM m")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, arg := range []any{2, nil, 2} {
				rows, err := stmt.QueryContext(p.Ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "BOOL" {
					t.Fatal(meta, err)
				}
				fixture.ScalarAssertMetadata(t, backend.PG, meta)
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
			betweenReferenceBoth(t, p, fixture.Select("SELECT "+expr+" FROM m", []string{"?column?"}, []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}}, 3))
		}
	}
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ form, op string }{{"BETWEEN", "<="}, {"NOT BETWEEN", ">"}, {"BETWEEN SYMMETRIC", "<="}, {"NOT BETWEEN SYMMETRIC", ">"}} {
		step := fixture.ImplicitError("SELECT $1 "+tc.form+" i AND s FROM m", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer " + tc.op + " text", Position: "11", Hint: hint}, 2)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}

func TestReferenceBetweenSyntaxDiagnostics(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, tc := range []struct{ q, message, pos string }{
		{"SELECT v BETWEEN 1 3 FROM t", `syntax error at or near "3"`, "20"},
		{"SELECT v BETWEEN AND 3 FROM t", `syntax error at or near "AND"`, "18"},
		{"SELECT v BETWEEN 1 AND", `syntax error at end of input`, "23"},
	} {
		step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: tc.pos})
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}

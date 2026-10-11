package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreBindOccurrenceResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT,f BOOL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,2,TRUE)", 1))
			q := "UPDATE t SET a=$1,b=$1"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			bad := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
			for _, prepared := range []bool{false, true} {
				if prepared {
					_, err = stmt.ExecContext(p.Ctx, "bad")
				} else {
					_, err = backend.DB.ExecContext(p.Ctx, q, "bad")
				}
				fixture.AssertDiagnostic(t, err, &bad)
				run(fixture.Select("SELECT a,b,f FROM t", []string{"a", "b", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(2), true}}))
			}
			for _, tc := range []struct{ q, pos string }{{"UPDATE t SET a=$1,f=$1", "21"}, {"UPDATE /*é*/ t SET a=$1,f=$1", "27"}} {
				for _, args := range [][]any{nil, {1}} {
					diagnostic := fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: tc.pos}
					if backend.PG && args == nil {
						diagnostic = fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "16"}
						if tc.pos == "27" {
							diagnostic.Position = "22"
						}
					}
					run(fixture.ImplicitError(tc.q, diagnostic, args...))
				}
				step := fixture.Prepared(fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: tc.pos}))
				step.ErrorStage = "prepare"
				run(step)
			}
			gap := fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}
			simple := gap
			if backend.PG {
				simple = fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "16"}
			}
			run(fixture.ImplicitError("UPDATE t SET a=$2", simple))
			step := fixture.Prepared(fixture.ImplicitError("UPDATE t SET a=$2", gap))
			step.ErrorStage = "prepare"
			run(step)
			run(fixture.ImplicitError("UPDATE t SET a=$2", gap, nil, 7))
			run(fixture.Exec(`CREATE SCHEMA "é space"`, 0))
			run(fixture.Exec(`CREATE TABLE "é space"."T""x"(id INT,"A" INT,b INT)`, 0))
			run(fixture.Exec(`INSERT INTO "é space"."T""x" VALUES(1,10,20),(2,30,40)`, 2))
			q = `UPDATE "é space"."T""x" SET "A"=$1,b="A" WHERE id=$2`
			run(fixture.Exec(q, 1, 7, 1))
			quoted, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer quoted.Close()
			for _, args := range [][]any{{9, 1}, {5, 2}} {
				result, err := quoted.ExecContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LockAffected(t, result, 1)
			}
			run(fixture.Select(`SELECT id,"A",b FROM "é space"."T""x" ORDER BY id`, []string{"id", "A", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(9), int64(7)}, {int64(2), int64(5), int64(30)}}))
			run(fixture.Exec("UPDATE t SET a=$1,b=$1", 1, 8))
			run(fixture.Prepared(fixture.Exec("UPDATE t SET a=$1,b=$1", 1, 9)))
			run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
		})
	}
}

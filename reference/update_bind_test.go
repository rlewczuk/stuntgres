package reference_test

import "testing"

func TestReferenceUpdateCoreBindOccurrenceResolution(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b INT,f BOOL)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,2,TRUE)", 1))
			q := "UPDATE t SET a=$1,b=$1"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			bad := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
			for _, prepared := range []bool{false, true} {
				if prepared {
					_, err = stmt.ExecContext(p.ctx, "bad")
				} else {
					_, err = backend.db.ExecContext(p.ctx, q, "bad")
				}
				referenceAssertDiagnostic(t, err, &bad)
				run(referenceSelect("SELECT a,b,f FROM t", []string{"a", "b", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(2), true}}))
			}
			for _, tc := range []struct{ q, pos string }{{"UPDATE t SET a=$1,f=$1", "21"}, {"UPDATE /*é*/ t SET a=$1,f=$1", "27"}} {
				for _, args := range [][]any{nil, {1}} {
					diagnostic := referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: tc.pos}
					if backend.pg && args == nil {
						diagnostic = referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "16"}
						if tc.pos == "27" {
							diagnostic.Position = "22"
						}
					}
					run(referenceImplicitError(tc.q, diagnostic, args...))
				}
				step := referencePrepared(referenceImplicitError(tc.q, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: tc.pos}))
				step.errorStage = "prepare"
				run(step)
			}
			gap := referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}
			simple := gap
			if backend.pg {
				simple = referenceDiagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "16"}
			}
			run(referenceImplicitError("UPDATE t SET a=$2", simple))
			step := referencePrepared(referenceImplicitError("UPDATE t SET a=$2", gap))
			step.errorStage = "prepare"
			run(step)
			run(referenceImplicitError("UPDATE t SET a=$2", gap, nil, 7))
			run(referenceExec(`CREATE SCHEMA "é space"`, 0))
			run(referenceExec(`CREATE TABLE "é space"."T""x"(id INT,"A" INT,b INT)`, 0))
			run(referenceExec(`INSERT INTO "é space"."T""x" VALUES(1,10,20),(2,30,40)`, 2))
			q = `UPDATE "é space"."T""x" SET "A"=$1,b="A" WHERE id=$2`
			run(referenceExec(q, 1, 7, 1))
			quoted, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer quoted.Close()
			for _, args := range [][]any{{9, 1}, {5, 2}} {
				result, err := quoted.ExecContext(p.ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, result, 1)
			}
			run(referenceSelect(`SELECT id,"A",b FROM "é space"."T""x" ORDER BY id`, []string{"id", "A", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(9), int64(7)}, {int64(2), int64(5), int64(30)}}))
			run(referenceExec("UPDATE t SET a=$1,b=$1", 1, 8))
			run(referencePrepared(referenceExec("UPDATE t SET a=$1,b=$1", 1, 9)))
			run(referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
		})
	}
}

//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBtreeIndexPreparedNamespaces(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		t.Cleanup(func() {
			if _, err := db.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS a,b CASCADE"); err != nil {
				t.Error("namespace cleanup", err)
			}
		})
		for _, step := range []referenceStep{referenceExec("CREATE SCHEMA a", 0), referenceExec("CREATE SCHEMA b", 0), referenceExec("CREATE TABLE a.t(n INT)", 0), referenceExec("CREATE TABLE b.t(n INT)", 0), referenceExec("SET search_path=a", 0)} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
		stmt, err := db.PrepareContext(p.ctx, "CREATE INDEX idx ON t(n)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceRunStep(t, p.ctx, db, recorder, referenceExec("SET search_path=b,a", 0))
		held := referenceHeldStatement{referenceExecutor: db, statement: stmt, query: "CREATE INDEX idx ON t(n)"}
		referenceRunStep(t, p.ctx, held, recorder, referenceExec("CREATE INDEX idx ON t(n)", 0))
		if db == p.memory {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			const expected = `CREATE INDEX "idx" ON "b"."t" USING btree ("n" ASC NULLS LAST);`
			if !strings.Contains(dump.String(), expected) {
				t.Fatal("prepared utility bound wrong namespace", dump.String())
			}
		} else {
			referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT c.relname,n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE c.relname='idx'", []string{"relname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"idx", "b"}}))
		}
		referenceRunStep(t, p.ctx, db, recorder, referencePrepared(referenceExec("CREATE INDEX fixed ON a.t(n)", 0)))
		referenceRunStep(t, p.ctx, db, recorder, referenceExec("DROP TABLE b.t", 0))
		referenceRunStep(t, p.ctx, db, recorder, referenceExec("CREATE TABLE b.t(n INT)", 0))
		referenceRunStep(t, p.ctx, held, recorder, referenceExec("CREATE INDEX idx ON t(n)", 0))
	})
}
func TestReferenceBtreeIndexNotices(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "postgres"}[postgres], func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, postgres, &notices)
			for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("CREATE TABLE collision(n INT)", 0), referenceExec("CREATE INDEX idx ON t(n)", 0)} {
				referenceRunStep(t, p.ctx, conn, recorder, step)
			}
			for _, name := range []string{"collision", "idx"} {
				referenceRunStep(t, p.ctx, conn, recorder, referencePrepared(referenceExec("CREATE INDEX IF NOT EXISTS "+name+" ON t(n)", 0)))
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "` + name + `" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices, want)
				}
				notices = nil
			}
			step := referencePrepared(referenceImplicitError("CREATE INDEX IF NOT EXISTS idx ON t(missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`}))
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, conn, recorder, step)
			if len(notices) != 0 {
				t.Fatal("bad key skipped", notices)
			}
		})
	}
}

func TestReferenceBtreeIndexSyntaxDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		for _, tc := range []struct{ query, message, position string }{
			{"CREATE INDEX s.i ON t(n)", `syntax error at or near "."`, "15"},
			// ')' is character 22; character 21 is the opening '('.
			{"CREATE INDEX i ON t ()", `syntax error at or near ")"`, "22"},
			{"/*é*/ CREATE INDEX s.i ON t(n)", `syntax error at or near "."`, "21"},
		} {
			step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42601", Message: tc.message, Position: tc.position})
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, db, recorder, step)
			step = referencePrepared(step)
			step.errorStage = "prepare"
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
	})
}

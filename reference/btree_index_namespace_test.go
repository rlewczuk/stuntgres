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
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			roleReferenceCleanup(t, []string{"reader"})
			p := newReferenceScenario(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
				db.SetMaxOpenConns(1)
				run := func(s referenceStep) {
					s.nonContext = nonContext
					referenceRunStep(t, p.ctx, db, recorder, s)
				}
				t.Cleanup(func() {
					if _, err := db.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS a,b CASCADE"); err != nil {
						t.Error("namespace cleanup", err)
					}
				})
				for _, step := range []referenceStep{referenceExec("CREATE SCHEMA a", 0), referenceExec("CREATE SCHEMA b", 0), referenceExec("CREATE TABLE a.t(n INT)", 0), referenceExec("CREATE TABLE b.t(n INT)", 0), referenceExec("SET search_path=a", 0), referenceExec("INSERT INTO a.t VALUES(1)", 1), referenceExec("INSERT INTO b.t VALUES(2)", 1)} {
					run(step)
				}
				var stmt *sql.Stmt
				var err error
				if nonContext {
					stmt, err = db.Prepare("CREATE INDEX idx ON t(n)")
				} else {
					stmt, err = db.PrepareContext(p.ctx, "CREATE INDEX idx ON t(n)")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(referenceExec("SET search_path=b,a", 0))
				var result sql.Result
				if nonContext {
					result, err = stmt.Exec()
				} else {
					result, err = stmt.ExecContext(p.ctx)
				}
				deleteReferenceCount(t, result, err, 0)
				run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}}))
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
					run(referenceSelect("SELECT c.relname,n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE c.relname='idx'", []string{"relname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"idx", "b"}}))
				}
				var fixed *sql.Stmt
				if nonContext {
					fixed, err = db.Prepare("CREATE INDEX fixed ON a.t(n)")
				} else {
					fixed, err = db.PrepareContext(p.ctx, "CREATE INDEX fixed ON a.t(n)")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer fixed.Close()
				if nonContext {
					result, err = fixed.Exec()
				} else {
					result, err = fixed.ExecContext(p.ctx)
				}
				deleteReferenceCount(t, result, err, 0)
				run(referenceExec("DROP TABLE b.t", 0))
				run(referenceExec("CREATE TABLE b.t(n INT)", 0))
				if nonContext {
					result, err = stmt.Exec()
				} else {
					result, err = stmt.ExecContext(p.ctx)
				}
				deleteReferenceCount(t, result, err, 0)
				run(referenceExec("CREATE USER reader", 0))
				var denied *sql.Stmt
				if nonContext {
					denied, err = db.Prepare("CREATE INDEX denied ON a.t(n)")
				} else {
					denied, err = db.PrepareContext(p.ctx, "CREATE INDEX denied ON a.t(n)")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer denied.Close()
				run(referenceExec("SET ROLE reader", 0))
				defer db.ExecContext(p.ctx, "RESET ROLE")
				if nonContext {
					result, err = denied.Exec()
				} else {
					result, err = denied.ExecContext(p.ctx)
				}
				if result != nil {
					t.Fatal("failed held Exec exposed a result")
				}
				want := controlsReferenceDiagnostic("42501", "permission denied for schema a")
				referenceAssertDiagnostic(t, err, &want)
				run(referenceExec("RESET ROLE", 0))
				if nonContext {
					result, err = denied.Exec()
				} else {
					result, err = denied.ExecContext(p.ctx)
				}
				if err != nil {
					t.Fatal("prepared utility retained failed authority", err)
				}
				deleteReferenceCount(t, result, err, 0)
			})
		})
	}
}
func TestReferenceBtreeIndexNotices(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		postgres := backend.pg
		t.Run(map[bool]string{false: "native", true: "postgres"}[postgres], func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, postgres, &notices)
			for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("CREATE TABLE collision(n INT)", 0), referenceExec("CREATE INDEX idx ON t(n)", 0)} {
				referenceRunStep(t, p.ctx, conn, recorder, step)
			}
			for _, prepared := range []bool{false, true} {
				for _, name := range []string{"collision", "idx"} {
					noticeStep := referenceExec("CREATE INDEX IF NOT EXISTS "+name+" ON t(n)", 0)
					noticeStep.prepared = prepared
					referenceRunStep(t, p.ctx, conn, recorder, noticeStep)
					want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "` + name + `" already exists, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(notices, want)
					}
					notices = nil
				}
			}
			step := referencePrepared(referenceImplicitError("CREATE INDEX IF NOT EXISTS idx ON t(missing)", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`}))
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, conn, recorder, step)
			if len(notices) != 0 {
				t.Fatal("bad key skipped", notices)
			}
			stmt, err := conn.PrepareContext(p.ctx, "CREATE INDEX IF NOT EXISTS idx ON t(missing)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.Exec()
			if result != nil {
				t.Fatal("failed notice utility exposed a result")
			}
			want := controlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			referenceAssertDiagnostic(t, err, &want)
			if len(notices) != 0 {
				t.Fatal("bad key incorrectly skipped", notices)
			}
		})
	}
}

func TestReferenceBtreeIndexSyntaxDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
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

package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexPreparedNamespaces(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"reader"})
			p := fixture.NewScenario(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
				db.SetMaxOpenConns(1)
				run := func(s fixture.Step) {
					s.NonContext = nonContext
					fixture.RunStep(t, p.Ctx, db, recorder, s)
				}
				t.Cleanup(func() {
					if _, err := db.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS a,b CASCADE"); err != nil {
						t.Error("namespace cleanup", err)
					}
				})
				for _, step := range []fixture.Step{fixture.Exec("CREATE SCHEMA a", 0), fixture.Exec("CREATE SCHEMA b", 0), fixture.Exec("CREATE TABLE a.t(n INT)", 0), fixture.Exec("CREATE TABLE b.t(n INT)", 0), fixture.Exec("SET search_path=a", 0), fixture.Exec("INSERT INTO a.t VALUES(1)", 1), fixture.Exec("INSERT INTO b.t VALUES(2)", 1)} {
					run(step)
				}
				var stmt *sql.Stmt
				var err error
				if nonContext {
					stmt, err = db.Prepare("CREATE INDEX idx ON t(n)")
				} else {
					stmt, err = db.PrepareContext(p.Ctx, "CREATE INDEX idx ON t(n)")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(fixture.Exec("SET search_path=b,a", 0))
				var result sql.Result
				if nonContext {
					result, err = stmt.Exec()
				} else {
					result, err = stmt.ExecContext(p.Ctx)
				}
				fixture.DeleteReferenceCount(t, result, err, 0)
				run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}}))
				if db == p.Memory {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					const expected = `CREATE INDEX "idx" ON "b"."t" USING btree ("n" ASC NULLS LAST);`
					if !strings.Contains(dump.String(), expected) {
						t.Fatal("prepared utility bound wrong namespace", dump.String())
					}
				} else {
					run(fixture.Select("SELECT c.relname,n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE c.relname='idx'", []string{"relname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"idx", "b"}}))
				}
				var fixed *sql.Stmt
				if nonContext {
					fixed, err = db.Prepare("CREATE INDEX fixed ON a.t(n)")
				} else {
					fixed, err = db.PrepareContext(p.Ctx, "CREATE INDEX fixed ON a.t(n)")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer fixed.Close()
				if nonContext {
					result, err = fixed.Exec()
				} else {
					result, err = fixed.ExecContext(p.Ctx)
				}
				fixture.DeleteReferenceCount(t, result, err, 0)
				run(fixture.Exec("DROP TABLE b.t", 0))
				run(fixture.Exec("CREATE TABLE b.t(n INT)", 0))
				if nonContext {
					result, err = stmt.Exec()
				} else {
					result, err = stmt.ExecContext(p.Ctx)
				}
				fixture.DeleteReferenceCount(t, result, err, 0)
				run(fixture.Exec("CREATE USER reader", 0))
				var denied *sql.Stmt
				if nonContext {
					denied, err = db.Prepare("CREATE INDEX denied ON a.t(n)")
				} else {
					denied, err = db.PrepareContext(p.Ctx, "CREATE INDEX denied ON a.t(n)")
				}
				if err != nil {
					t.Fatal(err)
				}
				defer denied.Close()
				run(fixture.Exec("SET ROLE reader", 0))
				defer db.ExecContext(p.Ctx, "RESET ROLE")
				if nonContext {
					result, err = denied.Exec()
				} else {
					result, err = denied.ExecContext(p.Ctx)
				}
				if result != nil {
					t.Fatal("failed held Exec exposed a result")
				}
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema a")
				fixture.AssertDiagnostic(t, err, &want)
				run(fixture.Exec("RESET ROLE", 0))
				if nonContext {
					result, err = denied.Exec()
				} else {
					result, err = denied.ExecContext(p.Ctx)
				}
				if err != nil {
					t.Fatal("prepared utility retained failed authority", err)
				}
				fixture.DeleteReferenceCount(t, result, err, 0)
			})
		})
	}
}
func TestReferenceBtreeIndexNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(map[bool]string{false: "native", true: "postgres"}[postgres], func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, postgres, &notices)
			for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("CREATE TABLE collision(n INT)", 0), fixture.Exec("CREATE INDEX idx ON t(n)", 0)} {
				fixture.RunStep(t, p.Ctx, conn, recorder, step)
			}
			for _, prepared := range []bool{false, true} {
				for _, name := range []string{"collision", "idx"} {
					noticeStep := fixture.Exec("CREATE INDEX IF NOT EXISTS "+name+" ON t(n)", 0)
					noticeStep.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, noticeStep)
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "` + name + `" already exists, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(notices, want)
					}
					notices = nil
				}
			}
			step := fixture.Prepared(fixture.ImplicitError("CREATE INDEX IF NOT EXISTS idx ON t(missing)", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`}))
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, recorder, step)
			if len(notices) != 0 {
				t.Fatal("bad key skipped", notices)
			}
			stmt, err := conn.PrepareContext(p.Ctx, "CREATE INDEX IF NOT EXISTS idx ON t(missing)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.Exec()
			if result != nil {
				t.Fatal("failed notice utility exposed a result")
			}
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			fixture.AssertDiagnostic(t, err, &want)
			if len(notices) != 0 {
				t.Fatal("bad key incorrectly skipped", notices)
			}
		})
	}
}

func TestReferenceBtreeIndexSyntaxDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
		for _, tc := range []struct{ query, message, position string }{
			{"CREATE INDEX s.i ON t(n)", `syntax error at or near "."`, "15"},
			// ')' is character 22; character 21 is the opening '('.
			{"CREATE INDEX i ON t ()", `syntax error at or near ")"`, "22"},
			{"/*é*/ CREATE INDEX s.i ON t(n)", `syntax error at or near "."`, "21"},
		} {
			step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: tc.position})
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, db, recorder, step)
			step = fixture.Prepared(step)
			step.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, db, recorder, step)
		}
	})
}

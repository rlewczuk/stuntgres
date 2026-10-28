package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintAlterSafety(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, action := range []struct{ name, sql string }{
			{"add", "ALTER TABLE c ADD COLUMN note TEXT"},
			{"type", "ALTER TABLE c ALTER COLUMN pid TYPE BIGINT"},
			{"enforcement", "ALTER TABLE c ALTER CONSTRAINT fk NOT ENFORCED"},
			{"set-default", "ALTER TABLE c ALTER COLUMN pid SET DEFAULT 1"},
			{"absent-drop-default", "ALTER TABLE c ALTER COLUMN pid DROP DEFAULT"},
		} {
			t.Run(fmt.Sprintf("valid-child/%s/prepared=%t", action.name, prepared), func(t *testing.T) {
				deferredAlterSafetyScenario(t, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
					run(fixture.Exec("SAVEPOINT s", 0))
					failure := fixture.ErrorDiagnostic(action.sql, fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "c" because it has pending trigger events`))
					if prepared {
						failure = fixture.Prepared(failure)
					}
					run(failure)
					run(fixture.Exec("ROLLBACK TO s", 0))
					run(fixture.Select("SELECT count(*) FROM c WHERE pid=1", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					success := fixture.Exec(action.sql, 0)
					if prepared {
						success = fixture.Prepared(success)
					}
					run(success)
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT count(*) FROM c WHERE pid=1", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				})
			})
		}
		for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
			t.Run(fmt.Sprintf("repaired-index-event/%s/prepared=%t", kind, prepared), func(t *testing.T) {
				deferredAlterSafetyScenario(t, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE t(v INT, marker INT, CONSTRAINT deferred_key "+kind+"(v) INITIALLY DEFERRED)", 0))
					run(fixture.Exec("BEGIN", 0))
					// The second row enlists a real potential-conflict event. Repair
					// the first row, not the event's row, so a valid obligation remains.
					run(fixture.Exec("INSERT INTO t VALUES(1,1),(1,2)", 2))
					run(fixture.Exec("DELETE FROM t WHERE marker=1", 1))
					run(fixture.Exec("SAVEPOINT s", 0))
					failure := fixture.ErrorDiagnostic("ALTER TABLE t ADD COLUMN note TEXT", fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "t" because it has pending trigger events`))
					if prepared {
						failure = fixture.Prepared(failure)
					}
					run(failure)
					run(fixture.Exec("ROLLBACK TO s", 0))
					run(fixture.Select("SELECT v,marker FROM t", []string{"v", "marker"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
					run(fixture.Exec("SET CONSTRAINTS deferred_key IMMEDIATE", 0))
					success := fixture.Exec("ALTER TABLE t ADD COLUMN note TEXT", 0)
					if prepared {
						success = fixture.Prepared(success)
					}
					run(success)
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT v,marker,note FROM t", []string{"v", "marker", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), nil}}))
				})
			})
			t.Run(fmt.Sprintf("uncontested-index/%s/prepared=%t", kind, prepared), func(t *testing.T) {
				deferredAlterSafetyScenario(t, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE t(v INT, CONSTRAINT deferred_key "+kind+"(v) INITIALLY DEFERRED)", 0))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					// Deferrability alone does not create a pending recheck.
					alter := fixture.Exec("ALTER TABLE t ADD COLUMN note TEXT", 0)
					if prepared {
						alter = fixture.Prepared(alter)
					}
					run(alter)
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT v,note FROM t", []string{"v", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
				})
			})
		}
		t.Run(fmt.Sprintf("parent-event-locality/prepared=%t", prepared), func(t *testing.T) {
			deferredAlterSafetyScenario(t, func(run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DELETE FROM p", 1))
				run(fixture.Exec("SAVEPOINT s", 0))
				failure := fixture.ErrorDiagnostic("ALTER TABLE p ADD COLUMN note TEXT", fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "p" because it has pending trigger events`))
				if prepared {
					failure = fixture.Prepared(failure)
				}
				run(failure)
				run(fixture.Exec("ROLLBACK TO s", 0))
				// A parent-side NO ACTION event does not belong to c.
				childAlter := fixture.Exec("ALTER TABLE c ADD COLUMN note TEXT", 0)
				if prepared {
					childAlter = fixture.Prepared(childAlter)
				}
				run(childAlter)
				run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
				parentAlter := fixture.Exec("ALTER TABLE p ADD COLUMN note TEXT", 0)
				if prepared {
					parentAlter = fixture.Prepared(parentAlter)
				}
				run(parentAlter)
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Select("SELECT p.id,c.pid FROM p JOIN c ON p.id=c.pid", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
			})
		})
		t.Run(fmt.Sprintf("rename-with-pending-event/prepared=%t", prepared), func(t *testing.T) {
			deferredAlterSafetyScenario(t, func(run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
				// Rename is a different utility path, not an AlterTableStmt.
				rename := fixture.Exec("ALTER TABLE c RENAME TO renamed_child", 0)
				if prepared {
					rename = fixture.Prepared(rename)
				}
				run(rename)
				run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Select("SELECT pid FROM renamed_child", []string{"pid"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

// deferredAlterSafetyScenario owns a pinned session for each admission witness.
func deferredAlterSafetyScenario(t *testing.T, body func(func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.ConcurrentIndexConn(t, p, backend)
			body(func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) })
		})
	}
}

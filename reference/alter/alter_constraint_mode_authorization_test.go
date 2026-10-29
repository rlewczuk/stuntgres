package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintAuthorization(t *testing.T) {
	t.Run("held_current_root_owner_and_read_only", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"mode_guest"})
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			constraintModeGraph(run)
			run(fixture.Exec("CREATE ROLE mode_guest", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO mode_guest", 0))
			for _, suffix := range []string{"", "INHERIT", "NO INHERIT"} {
				q := "ALTER TABLE t ALTER CONSTRAINT nn"
				if suffix != "" {
					q += " " + suffix
				}
				h := addConstraintHeld(t, p, b, c, q)
				before := constraintModeSnapshot(t, p, b, c)
				run(fixture.Exec("SET ROLE mode_guest", 0))
				validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t")))
				run(fixture.Exec("RESET ROLE", 0))
				constraintModeUnchanged(t, p, b, c, before)
				run(fixture.Exec("BEGIN READ ONLY", 0))
				validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction")))
				run(fixture.Exec("ROLLBACK", 0))
				constraintModeUnchanged(t, p, b, c, before)
			}
		})
	})
	t.Run("direction_specific_child_owner_and_branch_stop", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"mode_root_owner", "mode_child_owner"})
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			constraintModeGraph(run)
			for _, q := range []string{"CREATE ROLE mode_root_owner", "CREATE ROLE mode_child_owner", "GRANT USAGE ON SCHEMA public TO mode_root_owner", "ALTER TABLE t OWNER TO mode_root_owner", "ALTER TABLE child OWNER TO mode_child_owner", "ALTER TABLE grandchild OWNER TO mode_child_owner"} {
				run(fixture.Exec(q, 0))
			}
			detach := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT")
			attach := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn INHERIT")
			run(fixture.Exec("SET ROLE mode_root_owner", 0))
			// Released NO INHERIT localizes direct children through catalog edits;
			// it does not run the child SET NOT NULL ownership check.
			validateConstraintHeldRun(t, p, b, detach, fixture.Exec(detach.SQL, 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(constraintModeNN("child", "nn", false, true, 0, true))
			before := constraintModeSnapshot(t, p, b, c)
			run(fixture.Exec("SET ROLE mode_root_owner", 0))
			validateConstraintHeldRun(t, p, b, attach, fixture.ErrorDiagnostic(attach.SQL, fixture.ControlsReferenceDiagnostic("42501", "must be owner of table child")))
			run(fixture.Exec("RESET ROLE", 0))
			constraintModeUnchanged(t, p, b, c, before)
			run(fixture.Exec("ALTER TABLE child OWNER TO mode_root_owner", 0))
			run(fixture.Exec("SET ROLE mode_root_owner", 0))
			// Existing compatible child stops recursion: grandchild's independent
			// ownership cannot prevent the visited parent's merge.
			validateConstraintHeldRun(t, p, b, attach, fixture.Exec(attach.SQL, 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(constraintModeNN("child", "nn", false, true, 1, true))
			run(constraintModeNN("grandchild", "nn", false, false, 1, true))
		})
	})
	t.Run("new_child_owner_failure_is_atomic", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"mode_root_owner", "mode_child_owner"})
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			for _, q := range []string{"CREATE ROLE mode_root_owner", "CREATE ROLE mode_child_owner", "GRANT USAGE ON SCHEMA public TO mode_root_owner", "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT", "CREATE TABLE first_child() INHERITS(t)", "CREATE TABLE last_child() INHERITS(t)", "ALTER TABLE t OWNER TO mode_root_owner", "ALTER TABLE first_child OWNER TO mode_root_owner", "ALTER TABLE last_child OWNER TO mode_child_owner"} {
				run(fixture.Exec(q, 0))
			}
			before := constraintModeSnapshot(t, p, b, c)
			run(fixture.Exec("SET ROLE mode_root_owner", 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ALTER CONSTRAINT nn INHERIT", fixture.ControlsReferenceDiagnostic("42501", "must be owner of table last_child"))))
			run(fixture.Exec("RESET ROLE", 0))
			constraintModeUnchanged(t, p, b, c, before)
		})
	})
	for _, suffix := range []string{"", "INHERIT", "NO INHERIT"} {
		t.Run("pending_events/"+suffix, func(t *testing.T) {
			constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p INITIALLY DEFERRED)", 0))
				q := "ALTER TABLE t ALTER CONSTRAINT nn"
				if suffix != "" {
					q += " " + suffix
				}
				h := addConstraintHeld(t, p, b, c, q)
				before := constraintModeSnapshot(t, p, b, c)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
				run(fixture.Exec("SAVEPOINT s", 0))
				validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "t" because it has pending trigger events`)))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(constraintModeNN("t", "nn", false, true, 0, true))
				run(fixture.Exec("ROLLBACK", 0))
				constraintModeUnchanged(t, p, b, c, before)
				validateConstraintHeldRun(t, p, b, h, fixture.Exec(q, 0))
			})
		})
	}
}

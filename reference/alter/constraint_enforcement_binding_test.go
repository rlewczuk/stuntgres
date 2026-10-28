package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementBindingDependencies(t *testing.T) {
	for _, tc := range []struct{ name, setup, clause, code, message, detail, hint string }{
		{"unknown_check", "", "CHECK(missing>0)", "42703", `column "missing" does not exist`, "", ""},
		{"nonboolean_check", "", "CHECK(a)", "42804", "argument of CHECK must be type boolean, not type integer", "", ""},
		{"unknown_routine", "", "CHECK(enforcement_absent(a)>0)", "42883", "function enforcement_absent(integer) does not exist", "", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"bad_type_expression", "", "CHECK(a::date IS NULL)", "42846", "cannot cast type integer to date", "", ""},
		{"no_unique", "CREATE TABLE p(id INT)", "FOREIGN KEY(a) REFERENCES p(id)", "42830", `there is no unique constraint matching given keys for referenced table "p"`, "", ""},
		{"no_primary", "CREATE TABLE p(id INT UNIQUE)", "FOREIGN KEY(a) REFERENCES p", "42704", `there is no primary key for referenced table "p"`, "", ""},
		{"wrong_type", "CREATE TABLE p(id TEXT PRIMARY KEY)", "FOREIGN KEY(a) REFERENCES p(id)", "42804", `foreign key constraint "fact" cannot be implemented`, `Key columns "a" of the referencing table and "id" of the referenced table are of incompatible types: integer and text.`, ""},
		{"wrong_count", "CREATE TABLE p(a INT,b INT,UNIQUE(a,b))", "FOREIGN KEY(a) REFERENCES p(a,b)", "42830", "number of referencing and referenced columns for foreign key disagree", "", ""},
		{"missing_parent_column", "CREATE TABLE p(id INT PRIMARY KEY)", "FOREIGN KEY(a) REFERENCES p(missing)", "42703", `column "missing" referenced in foreign key constraint does not exist`, "", ""},
		{"missing_child_column", "CREATE TABLE p(id INT PRIMARY KEY)", "FOREIGN KEY(missing) REFERENCES p(id)", "42703", `column "missing" referenced in foreign key constraint does not exist`, "", ""},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%v", tc.name, prepared), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(a INT,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
					if tc.setup != "" {
						run(fixture.Exec(tc.setup, 0))
					}
					q := "ALTER TABLE t ADD CONSTRAINT fact " + tc.clause + " NOT ENFORCED"
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail, d.Hint = tc.detail, tc.hint
					// REL_18_6 ATAddCheckConstraint passes NULL queryString to
					// AddRelationNewConstraints: these ALTER binding errors have no Position.
					before := enforcementSnapshot(t, p, b, c)
					addConstraintError(t, p, c, q, d, prepared, "execute")
					enforcementUnchanged(t, p, b, c, before)
				})
			})
		}
	}
	t.Run("references_and_visibility", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"enforcement_builder", "enforcement_reader"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			for _, q := range []string{"CREATE ROLE enforcement_builder", "CREATE ROLE enforcement_reader", "CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT)", "ALTER TABLE t OWNER TO enforcement_builder", "GRANT USAGE ON SCHEMA public TO enforcement_builder,enforcement_reader", "SET ROLE enforcement_builder"} {
				run(fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED")
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table p")))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT REFERENCES(id) ON p TO enforcement_builder", 0))
			run(fixture.Exec("SET ROLE enforcement_builder", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(fixture.Select("SELECT constraint_name,enforced FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='t' ORDER BY constraint_name", []string{"constraint_name", "enforced"}, []string{"NAME", "VARCHAR"}, [][]any{{"fk", "NO"}}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE REFERENCES(id) ON p FROM enforcement_builder", 0))
			run(fixture.Exec("SET ROLE enforcement_builder", 0))
			// Toggling an authenticated stored FK is not a new REFERENCES declaration.
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE enforcement_reader", 0))
			q := "SELECT constraint_name,enforced FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='t' ORDER BY constraint_name"
			run(fixture.Select(q, []string{"constraint_name", "enforced"}, []string{"NAME", "VARCHAR"}, nil))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT INSERT ON t TO enforcement_reader", 0))
			run(fixture.Exec("SET ROLE enforcement_reader", 0))
			run(fixture.Select(q, []string{"constraint_name", "enforced"}, []string{"NAME", "VARCHAR"}, [][]any{{"fk", "YES"}}))
			run(fixture.Exec("RESET ROLE", 0))
		})
	})
	for _, dependency := range []string{"function", "index"} {
		t.Run("dependencies/"+dependency, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "enforcement_moved")
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				q, detail, message, edge := "DROP FUNCTION enforcement_positive(INT)", "constraint ck on table t depends on function enforcement_positive(integer)", "cannot drop function enforcement_positive(integer) because other objects depend on it", "SELECT count(*) AS n FROM pg_depend WHERE classid='pg_constraint'::regclass AND objid=(SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass AND conname='ck') AND refclassid='pg_proc'::regclass AND refobjid='enforcement_positive(integer)'::regprocedure AND deptype='n'"
				if dependency == "function" {
					run(fixture.Exec("CREATE FUNCTION enforcement_positive(INT) RETURNS BOOL LANGUAGE SQL IMMUTABLE RETURN $1>0", 0))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(enforcement_positive(a)) NOT ENFORCED", 0))
				} else {
					// An explicit parent without another unique provider makes the edge unambiguous.
					run(fixture.Exec("CREATE TABLE target(id INT)", 0))
					run(fixture.Exec("CREATE UNIQUE INDEX target_key ON target(id)", 0))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES target(id) NOT ENFORCED", 0))
					q, detail, message = "DROP INDEX target_key", "constraint fk on table t depends on index target_key", "cannot drop index target_key because other objects depend on it"
					edge = "SELECT count(*) AS n FROM pg_depend WHERE classid='pg_constraint'::regclass AND objid=(SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass AND conname='fk') AND refclassid='pg_class'::regclass AND refobjid='target_key'::regclass AND deptype='n'"
				}
				run(fixture.Select(edge, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				d := fixture.ControlsReferenceDiagnostic("2BP01", message)
				d.Detail, d.Hint = detail, "Use DROP ... CASCADE to drop the dependent objects too."
				before := enforcementSnapshot(t, p, b, c)
				run(fixture.Prepared(fixture.ErrorDiagnostic(q, d)))
				enforcementUnchanged(t, p, b, c, before)
				if dependency == "index" {
					run(fixture.Exec("ALTER TABLE target RENAME TO renamed", 0))
					run(fixture.Exec("CREATE SCHEMA enforcement_moved", 0))
					run(fixture.Exec("ALTER TABLE renamed SET SCHEMA enforcement_moved", 0))
					enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES enforcement_moved.renamed(id) NOT ENFORCED", false, false)
					q = "DROP INDEX enforcement_moved.target_key"
				}
				run(fixture.Exec(q+" CASCADE", 0))
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass", []string{"conname"}, []string{"NAME"}, nil))
				run(fixture.Exec("INSERT INTO t VALUES(0,'dependency removed')", 1))
			})
		})
	}
}

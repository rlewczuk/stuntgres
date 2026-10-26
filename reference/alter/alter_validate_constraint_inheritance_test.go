package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintInheritance(t *testing.T) {
	t.Run("already_valid_child_not_rescanned", validateConstraintValidChild)
	for _, nn := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "check", true: "not_null"}[nn]+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE root(a INT)", "CREATE TABLE left_child() INHERITS(root)", "CREATE TABLE right_child() INHERITS(root)", "CREATE TABLE leaf() INHERITS(left_child,right_child)"} {
						run(fixture.Exec(q, 0))
					}
					bad := any(int64(0))
					clause := "CONSTRAINT fact CHECK(a>0) NOT VALID"
					if nn {
						bad = nil
						clause = "CONSTRAINT fact NOT NULL a NOT VALID"
					}
					run(fixture.Exec("INSERT INTO root VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO left_child VALUES(2)", 1))
					run(fixture.Exec("INSERT INTO leaf VALUES($1)", 1, bad))
					run(fixture.Exec("ALTER TABLE root ADD "+clause, 0))
					before := validateConstraintSnapshot(t, p, c)
					q := "ALTER TABLE root VALIDATE CONSTRAINT fact"
					held := addConstraintHeld(t, p, b, c, q)
					d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "fact" of relation "leaf" is violated by some row`)
					d.Schema, d.Table, d.Constraint = "public", "leaf", "fact"
					if nn {
						d = fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "leaf" contains null values`)
						d.Schema, d.Table, d.Column = "public", "leaf", "a"
					}
					run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE root ADD CONSTRAINT companion CHECK(a>=0) NOT VALID, VALIDATE CONSTRAINT fact", d)))
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
						t.Fatal("late leaf failure retained a companion definition or promotion")
					}
					if prepared {
						validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(q, d))
					} else {
						run(fixture.ErrorDiagnostic(q, d))
					}
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
						t.Fatal("late descendant failure published prefix")
					}
					run(fixture.Exec("UPDATE leaf SET a=3", 1))
					// Direct child validation cannot localize or promote its ancestors.
					run(fixture.Prepared(fixture.Exec("ALTER TABLE leaf VALIDATE CONSTRAINT fact", 0)))
					validateConstraintOrigins(t, p, c, before)
					run(fixture.Select("SELECT conislocal,coninhcount,convalidated FROM pg_constraint WHERE conrelid='leaf'::regclass AND conname='fact'", []string{"conislocal", "coninhcount", "convalidated"}, []string{"BOOL", "INT2", "BOOL"}, [][]any{{false, int64(2), true}}))
					run(validateConstraintFlag("root", "fact", false))
					for _, table := range []string{"left_child", "right_child"} {
						run(fixture.Exec("ALTER TABLE "+table+" VALIDATE CONSTRAINT fact", 0))
					}
					// ONLY still fails when every child has already been validated.
					run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE ONLY root VALIDATE CONSTRAINT fact", fixture.ControlsReferenceDiagnostic("42P16", "constraint must be validated on child tables too"))))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
					validateConstraintOrigins(t, p, c, before)
					run(fixture.Select("SELECT r.relname,c.convalidated FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE c.conname='fact' ORDER BY r.relname", []string{"relname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"leaf", true}, {"left_child", true}, {"right_child", true}, {"root", true}}))
					run(fixture.Exec("ALTER TABLE ONLY root VALIDATE CONSTRAINT fact", 0))
					run(fixture.Select("SELECT a FROM root ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
				})
			})
		}
	}
	t.Run("not_null_child_name_attribute_mapping", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE root(a INT,note TEXT)", 0))
			run(fixture.Exec("CREATE TABLE child(note TEXT,a INT)", 0))
			run(fixture.Exec("ALTER TABLE child INHERIT root", 0))
			run(fixture.Exec("INSERT INTO child VALUES('old',NULL)", 1))
			run(fixture.Exec("ALTER TABLE child ADD CONSTRAINT child_nn NOT NULL a NOT VALID", 0))
			run(fixture.Exec("ALTER TABLE root ADD CONSTRAINT parent_nn NOT NULL a NOT VALID", 0))
			before := validateConstraintSnapshot(t, p, c)
			d := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "child" contains null values`)
			d.Schema, d.Table, d.Column = "public", "child", "a"
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE root VALIDATE CONSTRAINT parent_nn", d)))
			if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
				t.Fatal("NN descendant failure changed origins")
			}
			run(fixture.Exec("UPDATE child SET a=7", 1))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE root VALIDATE CONSTRAINT parent_nn", 0)))
			run(validateConstraintFlag("child", "child_nn", true))
			run(validateConstraintFlag("root", "parent_nn", true))
			validateConstraintOrigins(t, p, c, before)
			run(fixture.Select("SELECT note,a FROM child", []string{"note", "a"}, []string{"TEXT", "INT4"}, [][]any{{"old", int64(7)}}))
		})
	})
	t.Run("no_inherit_check", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE root(a INT)", 0))
			run(fixture.Exec("CREATE TABLE child() INHERITS(root)", 0))
			run(fixture.Exec("INSERT INTO root VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO child VALUES(0)", 1))
			run(fixture.Exec("ALTER TABLE root ADD CONSTRAINT ck CHECK(a>0) NO INHERIT NOT VALID", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE ONLY root VALIDATE CONSTRAINT ck", 0)))
			run(validateConstraintFlag("root", "ck", true))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='child'::regclass", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT a FROM root ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
		})
	})
	t.Run("foreign_key_is_root_local", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'root')", 1))
			run(fixture.Exec("INSERT INTO child VALUES(9,'orphan')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
			run(fixture.Exec("ALTER TABLE child ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT fk", 0)))
			run(validateConstraintFlag("t", "fk", true))
			run(validateConstraintFlag("child", "fk", false))
			run(fixture.ErrorDiagnostic("ALTER TABLE child VALIDATE CONSTRAINT fk", addConstraintFK("child", "fk", "a", "9", "p")))
		})
	})
	t.Run("recursive_owner_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"validate_root_owner", "validate_child_owner"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE validate_root_owner", "CREATE ROLE validate_child_owner", "GRANT USAGE ON SCHEMA public TO validate_root_owner", "CREATE TABLE root(a INT)", "CREATE TABLE child() INHERITS(root)", "ALTER TABLE root ADD CONSTRAINT ck CHECK(a>0) NOT VALID", "ALTER TABLE child OWNER TO validate_child_owner", "ALTER TABLE root OWNER TO validate_root_owner", "SET ROLE validate_root_owner"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Prepared(fixture.Exec("ALTER TABLE root VALIDATE CONSTRAINT ck", 0)))
			run(fixture.Exec("RESET ROLE", 0))
			run(validateConstraintFlag("child", "ck", true))
			run(validateConstraintFlag("root", "ck", true))
		})
	})
}

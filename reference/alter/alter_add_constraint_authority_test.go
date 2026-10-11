package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func addConstraintAuthority(t *testing.T) {
	t.Run("owner_references_held_acl_and_disclosure", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"add_builder", "add_writer"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE add_builder", "CREATE ROLE add_writer", "CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", "GRANT REFERENCES(a) ON p TO add_builder"} {
				run(fixture.Exec(q, 0))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO add_builder", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO add_writer", 0))
			}
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
			}()
			run(fixture.Exec("SET ROLE add_builder", 0))
			run(fixture.Exec("CREATE TABLE c(a INT,b INT)", 0))
			q := "ALTER TABLE c ADD CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p NOT VALID"
			held := addConstraintHeld(t, p, b, c, q)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table p")))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT REFERENCES(b) ON p TO add_builder", 0))
			run(fixture.Exec("SET ROLE add_builder", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			run(fixture.Exec("RESET ROLE", 0))
			for _, q := range []string{"REVOKE REFERENCES(a,b) ON p FROM add_builder", "GRANT INSERT ON c TO add_writer", "GRANT DELETE ON p TO add_writer"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO p VALUES(1,2)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1,2)", 1))
			run(fixture.Exec("SET ROLE add_writer", 0))
			for _, prepared := range []bool{false, true} {
				d := addConstraintFK("c", "fk", "a, b", "9, 9", "p")
				d.Detail = `Key is not present in table "p".`
				s := fixture.ErrorDiagnostic("INSERT INTO c VALUES(9,9)", d)
				s.Prepared = prepared
				run(s)
				d = fixture.ControlsReferenceDiagnostic("23503", `update or delete on table "p" violates foreign key constraint "fk" on table "c"`)
				d.Detail, d.Schema, d.Table, d.Constraint = `Key is still referenced from table "c".`, "public", "c", "fk"
				s = fixture.ErrorDiagnostic("DELETE FROM p", d)
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("INSERT INTO c VALUES(1,2)", 1))
			addConstraintError(t, p, c, "ALTER TABLE c ADD CONSTRAINT ck CHECK(a>0)", fixture.ControlsReferenceDiagnostic("42501", "must be owner of table c"), true, "execute")
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}}))
		})
	})
	t.Run("binding_provider_records", func(t *testing.T) {
		for _, tc := range []struct{ setup, query, code, message, detail string }{
			{"CREATE TABLE p(id INT)", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT VALID", "42830", `there is no unique constraint matching given keys for referenced table "p"`, ""},
			{"CREATE TABLE p(id INT UNIQUE)", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", "42704", `there is no primary key for referenced table "p"`, ""},
			{"CREATE TABLE p(id TEXT PRIMARY KEY)", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT VALID", "42804", `foreign key constraint "fk" cannot be implemented`, `Key columns "a" of the referencing table and "id" of the referenced table are of incompatible types: integer and text.`},
			{"", "ALTER TABLE t ADD CONSTRAINT ck CHECK(missing>0)", "42703", `column "missing" does not exist`, ""},
			{"", "ALTER TABLE t ADD CONSTRAINT ck CHECK(a)", "42804", "argument of CHECK must be type boolean, not type integer", ""},
			{"", "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE USING gin(a WITH =)", "0A000", `access method "gin" does not support exclusion constraints`, ""},
			{"", "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE USING btree(a WITH <)", "42809", `operator <(integer,integer) is not commutative`, "Only commutative operators can be used in exclusion constraints."},
		} {
			t.Run(tc.query, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
					if tc.setup != "" {
						run(fixture.Exec(tc.setup, 0))
					}
					for _, prepared := range []bool{false, true} {
						d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						d.Detail = tc.detail
						addConstraintError(t, p, c, tc.query, d, prepared, "execute")
						run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
						run(addConstraintRows([]any{int64(1), "keep"}))
					}
				})
			})
		}
	})
	t.Run("generated_suffix_and_multiple_primary_key", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE TABLE occupied(a INT CONSTRAINT t_a_check CHECK(a>0))", "CREATE TABLE t(a INT,note TEXT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("ALTER TABLE t ADD CHECK(a>0),ADD CHECK(a<10)", 0))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"t_a_check1"}, {"t_a_check2"}}))
			run(fixture.Exec("ALTER TABLE t ADD PRIMARY KEY(a)", 0))
			addConstraintError(t, p, c, "ALTER TABLE t ADD CONSTRAINT second PRIMARY KEY(a)", fixture.ControlsReferenceDiagnostic("42P16", `multiple primary keys for table "t" are not allowed`), true, "execute")
		})
	})
}

func addConstraintHierarchy(t *testing.T) {
	for _, partition := range []bool{false, true} {
		t.Run(fmt.Sprint("hierarchy_", partition), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				if partition {
					run(fixture.Exec("CREATE TABLE parent(a INT) PARTITION BY RANGE(a)", 0))
					run(fixture.Exec("CREATE TABLE child PARTITION OF parent FOR VALUES FROM(0) TO(10)", 0))
				} else {
					run(fixture.Exec("CREATE TABLE parent(a INT)", 0))
					run(fixture.Exec("CREATE TABLE child() INHERITS(parent)", 0))
				}
				run(fixture.Exec("INSERT INTO child VALUES(1)", 1))
				q := "ALTER TABLE parent ADD CONSTRAINT ck CHECK(a>0)"
				if b.PG {
					run(fixture.Prepared(fixture.Exec(q, 0)))
					run(fixture.ErrorDiagnostic("INSERT INTO child VALUES(0)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "child" violates check constraint "ck"`, Detail: "Failing row contains (0).", Schema: "public", Table: "child", Constraint: "ck"}))
				} else {
					message := "ALTER TABLE inheritance operations are unsupported"
					if partition {
						message = "altering partition hierarchies is unsupported"
					}
					run(fixture.Prepared(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", message))))
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='parent'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
				}
				run(fixture.Select("SELECT a FROM child", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

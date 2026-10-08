//go:build reference

package reference_test

import "testing"

func TestReferenceQueryLimitViewBounds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceLimitFixture(t)
			p.step(t, referenceExec("DROP SCHEMA IF EXISTS bounds_schema CASCADE", 0))
			p.step(t, referenceExec("DROP SCHEMA IF EXISTS renamed_bounds CASCADE", 0))
			for _, step := range []referenceStep{
				referenceExec("CREATE SCHEMA bounds_schema", 0), referenceExec("CREATE TABLE bounds_schema.src(id INT)", 0),
				referenceExec("INSERT INTO bounds_schema.src VALUES(1),(2),(3),(4)", 4), referenceExec("CREATE SEQUENCE bounds_schema.s START WITH 1", 0),
				referenceExec("CREATE VIEW bounds_schema.dynamic AS SELECT id FROM bounds_schema.src ORDER BY id OFFSET NULL LIMIT (nextval('bounds_schema.s')+1)", 0),
				referenceExec("CREATE VIEW bounds_schema.unlimited AS SELECT id FROM bounds_schema.src LIMIT ALL OFFSET NULL", 0),
				referenceExec("CREATE VIEW bounds_schema.empty AS SELECT id FROM bounds_schema.src LIMIT (1-1)", 0),
				referenceExec("ALTER SCHEMA bounds_schema RENAME TO renamed_bounds", 0), referenceExec("ALTER TABLE renamed_bounds.src RENAME COLUMN id TO renamed", 0),
				referenceExec("ALTER TABLE renamed_bounds.src RENAME TO renamed_src", 0),
				referenceExec("ALTER SEQUENCE renamed_bounds.s RENAME TO renamed_s", 0),
			} {
				p.step(t, step)
			}
			execute := func(step referenceStep) {
				t.Helper()
				if prepared {
					step = referencePrepared(step)
				}
				p.step(t, step)
			}
			execute(referenceSelect("SELECT id FROM renamed_bounds.dynamic ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2)))
			execute(referenceSelect("SELECT last_value FROM renamed_bounds.renamed_s", []string{"last_value"}, []string{"INT8"}, referenceLimitIDs(1)))
			execute(referenceSelect("SELECT id FROM renamed_bounds.unlimited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4)))
			execute(referenceSelect("SELECT id FROM renamed_bounds.empty", []string{"id"}, []string{"INT4"}, referenceLimitIDs()))
			execute(referenceSelect("SELECT id FROM renamed_bounds.dynamic ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3)))
			// Public-schema object names make the existing dependency diagnostic
			// convention independently comparable, without widening qualification.
			p.step(t, referenceExec("CREATE SEQUENCE dep_s", 0))
			p.step(t, referenceExec("CREATE VIEW dependent AS SELECT id FROM t LIMIT nextval('dep_s')", 0))
			p.step(t, referenceImplicitError("DROP SEQUENCE dep_s RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence dep_s because other objects depend on it", Detail: "view dependent depends on sequence dep_s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
			p.step(t, referenceExec("DROP SCHEMA renamed_bounds CASCADE", 0))
		})
	}
}

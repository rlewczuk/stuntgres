package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitInsertAssignment(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceLimitFixture(t)
			p.Step(t, fixture.Exec("CREATE TABLE dst(id SMALLINT)", 0))
			execute := func(step fixture.Step) {
				t.Helper()
				if step.SelectRows {
					p.Step(t, step)
					p.Step(t, fixture.Prepared(step))
					return
				}
				if prepared {
					step = fixture.Prepared(step)
				}
				p.Step(t, step)
			}
			execute(fixture.Exec("INSERT INTO dst VALUES(1),(32768) LIMIT 1", 1))
			execute(fixture.Exec("INSERT INTO dst VALUES(32768) LIMIT 0", 0))
			execute(fixture.Exec("INSERT INTO dst VALUES(2),(32768),(3) OFFSET 2 LIMIT 1", 1))
			step := fixture.ImplicitError("INSERT INTO dst VALUES(2),(32768) LIMIT 2", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
			step.ErrorStage = "execute"
			execute(step)
			execute(fixture.Select("SELECT id FROM dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 3)))
			p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
			p.Step(t, fixture.Exec("CREATE TABLE defaults(id INT,n BIGINT DEFAULT 7)", 0))
			execute(fixture.Exec("INSERT INTO defaults(id) SELECT nextval('s') FROM t OFFSET 1 LIMIT 1", 1))
			execute(fixture.Exec("INSERT INTO defaults(id) SELECT nextval('s') FROM t LIMIT 0", 0))
			execute(fixture.Select("SELECT n FROM defaults", []string{"n"}, []string{"INT8"}, referenceLimitIDs(7)))
			execute(fixture.Select("SELECT id FROM defaults", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2)))
			sequence := fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(3))
			// The original sentinel is direct after either write route.
			p.Step(t, sequence)
			// Re-establish independent state for the reference prepared witness.
			p.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 3", 0))
			p.Step(t, fixture.Prepared(sequence))
			p.Step(t, fixture.Exec("CREATE TABLE failing_default(id INT,n INT DEFAULT 1/0)", 0))
			for _, suffix := range []string{"LIMIT 0", "LIMIT 1"} {
				step = fixture.ImplicitError("INSERT INTO failing_default(id) SELECT id FROM t "+suffix, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				step.ErrorStage = "execute"
				execute(step)
				execute(fixture.Select("SELECT id FROM failing_default", []string{"id"}, []string{"INT4"}, referenceLimitIDs()))
			}
			execute(fixture.Exec("INSERT INTO t SELECT id,v FROM t ORDER BY id LIMIT 1", 1))
			execute(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 1, 2, 3, 4)))
		})
	}
}

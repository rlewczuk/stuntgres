package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningNamespaceErrors(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1)", 1))
	p.Step(t, fixture.ImplicitError("UPDATE t SET id=2 RETURNING id AS label,label", fixture.Diagnostic{Code: "42703", Message: `column "label" does not exist`, Position: "41"}))
	p.Step(t, fixture.Exec("ALTER TABLE t DROP COLUMN id", 0))
	p.Step(t, fixture.Prepared(fixture.ImplicitError("UPDATE t SET absent=1 RETURNING *", fixture.Diagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: "33"})))
}

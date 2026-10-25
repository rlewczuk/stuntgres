package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SQL only constructs the ordinary internal catalog fixture. Assertions exercise
// real binders, frozen presentation, provider validation and publication directly.
func policyUnitFixture(t *testing.T) (*core.Engine, *session.Operation, func(), *catalog.Relation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	roleExecute(t, e, op, "CREATE TABLE t(id INTEGER, allowed BOOLEAN)")
	roleExecute(t, e, op, "CREATE TABLE helper(id INTEGER)")
	ref := roleTableRef(t, e, "t")
	d, _ := e.CatalogRead().Descriptor(ref.Address)
	return e, op, done, d.(*catalog.Relation)
}

func policyUnitBind(t *testing.T, e *core.Engine, op *session.Operation, d *catalog.Relation, condition string) (*stored.Policy, error) {
	t.Helper()
	raw := statement(t, "CREATE POLICY p ON t USING("+condition+")").(*parser.CreatePolicy)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return viewcmds.ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op}}}.BindPolicyExpression(auth, d, raw.Using, sqltype.InputEnvironment{})
}

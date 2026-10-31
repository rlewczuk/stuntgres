package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

// SQL only constructs the ordinary internal catalog fixture. Assertions exercise
// real binders, frozen presentation, provider validation and publication directly.
func policyUnitFixture(t *testing.T) (*core.Engine, *session.Operation, func(), *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id INTEGER, allowed BOOLEAN)")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE helper(id INTEGER)")
	ref := enginetest.RoleTableRef(t, e, "t")
	d, _ := e.CatalogRead().Descriptor(ref.Address)
	return e, op, done, d.(*catalog.Relation)
}

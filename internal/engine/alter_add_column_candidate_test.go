package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func alterAddUnitScope(t *testing.T) (*core.Engine, *core.StatementScope, core.AuthorizationView, *catalog.Relation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scope.Finish(errors.New("unit candidate discarded")) })
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	u := testUsage(e, sqltype.Int4)
	old := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, Owner: auth.State.CurrentUser, Columns: []catalog.Column{{Name: "a", Usage: u, Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 1}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Index: map[string]int{"a": 0}}
	return e, scope, auth, old
}

func alterAddUnitAction(t *testing.T, query string) parser.AlterTableAction {
	t.Helper()
	statements, err := parser.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	return statements[0].(*parser.AlterTable).Actions[0]
}

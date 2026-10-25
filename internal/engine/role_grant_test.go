package engine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These fixtures exercise the private catalog planner directly, not public SQL.
func objectGrantUnitFixture(t *testing.T) (*Engine, *session.Operation, authorization.PrincipalRef, ids.ObjectAddress) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	reader := roleAttributeCreate(t, e, op, "grant_reader", true, parser.RoleOptions{}).Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900001}
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	typ, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4 fixture type")
	}
	relation := &catalog.Relation{ObjectAddress: address, ObjectNamespace: namespace, ObjectName: "grant_unit", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: op.RoleState().CurrentUser, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}}, Index: map[string]int{"a": 0, "b": 1}, Attributes: []int32{1, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}}
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetDescriptor(address, relation)
	e.Catalog.State.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: relation.Name()}, address)
	return e, op, reader, address
}

func applyObjectGrantUnit(e *Engine, op *session.Operation, s *parser.ObjectGrant) error {
	_, err := e.Execute(op, s, 0, nil)
	return err
}

func objectGrantUnitRequest() *parser.ObjectGrant {
	return &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select", Columns: []parser.Identifier{{Text: "b"}}}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "grant_unit"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}}
}

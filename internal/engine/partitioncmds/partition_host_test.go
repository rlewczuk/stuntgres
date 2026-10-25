package partitioncmds_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionKeyExpression(t *testing.T, text string) parser.Expr {
	t.Helper()
	raw, err := parser.Parse("CREATE TABLE p(k int) PARTITION BY RANGE ((" + text + "))")
	if err != nil {
		t.Fatal(err)
	}
	return raw[0].(*parser.CreateTable).PartitionBy.Keys[0].Expression
}

func TestPartitionHostBindKeyExpressionReturnsKeyAndUsedSlots(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	var _ partition.Host = h
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	key, uses, err := h.BindKeyExpression(auth, f.parent, partitionKeyExpression(t, "k + 1"), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if key.Definition == nil || key.Execution == nil || !slices.Equal(key.Attributes, f.parent.Attributes) {
		t.Fatalf("derived key %#v, want definition, execution and the parent's attributes", key)
	}
	if key.Execution.ResultUsage().Type.ID() != sqltype.Int4 || !key.Execution.Properties().RowDependent {
		t.Fatalf("execution usage %v row dependent %v, want a row-dependent integer", key.Execution.ResultUsage(), key.Execution.Properties().RowDependent)
	}
	if !slices.Equal(uses, []int{0}) {
		t.Fatalf("used slots %v, want [0]", uses)
	}
}

// The volatility, constant and generated-column checks belong to
// partition.BindKeys: the host binds a volatile key and returns it.
func TestPartitionHostBindKeyExpressionLeavesVolatilityToCaller(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	key, uses, err := h.BindKeyExpression(auth, f.parent, partitionKeyExpression(t, "random()"), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if exec.PlannedVolatility(key.Execution) == expr.Immutable || key.Execution.Properties().RowDependent || len(uses) != 0 {
		t.Fatalf("volatile key: volatility %v row dependent %v uses %v", exec.PlannedVolatility(key.Execution), key.Execution.Properties().RowDependent, uses)
	}
}

func indexHostFixture(t *testing.T) (engine.TableHost, engine.AuthorizationView, partialIndexFixture) {
	t.Helper()
	f := newPartialIndexFixture(t)
	op, done := enginetest.RoleOperation(t, f.e, "postgres")
	t.Cleanup(done)
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stage := f.e.BeginCatalogStage(auth.Catalog)
	t.Cleanup(func() { f.e.DiscardCatalogStage(stage) })
	return engine.TableHost{DDLHost: engine.DDLHost{E: f.e, Op: op, CatalogStage: stage}}, auth, f
}

type partialIndexFixture struct {
	e             *engine.Engine
	parent, index *catalog.Relation
	predicate     plan.ColumnExpr
	store         *storage.Relation
}

func newPartialIndexFixture(t testing.TB) partialIndexFixture {
	t.Helper()
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{10, 20}, ColumnMeta: catalog.ColumnMetadata{HighWater: 20}, Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}, {Name: "active", Usage: boolean.Usage()}}, Index: map[string]int{"k": 0, "active": 1}}
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), parent.Address())
	ref.Address.SubID = 20
	column := plan.ColumnExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 1, Origin: ref}
	predicate := &catalog.IndexPredicate{Definition: column, Execution: column, Attributes: parent.Attributes, Dependencies: []ids.ObjectRef{ref}}
	class, _ := sqltype.DefaultIndexClass(integer)
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: parent.Namespace(), ObjectName: "pi", ObjectRevision: 1, RelationKind: catalog.IndexRelation, Columns: []catalog.Column{parent.Columns[0]}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}, SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Method: catalog.BtreeMethod, MethodOID: 403, Ready: true, Valid: true, Live: true, Keys: []catalog.IndexKey{{Attribute: 10, Class: class}}, Predicate: predicate}}
	store := &storage.Relation{}
	for i, k := range []int64{10, 20, 30, 0, 10} {
		value, _ := sqltype.Integer(integer, k)
		if i == 3 {
			value = sqltype.Null(integer)
		}
		active := sqltype.Boolean(boolean, i == 0 || i == 3 || i == 4)
		if i == 2 {
			active = sqltype.Null(boolean)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Values: []sqltype.Value{value, active}})
	}
	e.Catalog.State.SetDescriptor(parent.Address(), parent)
	e.Catalog.State.SetDescriptor(idx.Address(), idx)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(parent.Address(), store)
	e.Mu.Unlock()
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root})
	e.Mu.Unlock()
	return partialIndexFixture{e, parent, idx, column, store}
}

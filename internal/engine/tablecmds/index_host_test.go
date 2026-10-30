package tablecmds_test

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func indexHostFixture(t *testing.T) (tablecmds.TableHost, core.AuthorizationView, partialIndexFixture) {
	t.Helper()
	f := newPartialIndexFixture(t)
	op, done := roleOperation(t, f.e, "postgres")
	t.Cleanup(done)
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stage := f.e.BeginCatalogStage(auth.Catalog)
	t.Cleanup(func() { f.e.DiscardCatalogStage(stage) })
	return tablecmds.TableHost{core.DDLHost{E: f.e, Op: op, CatalogStage: stage}}, auth, f
}

func TestIndexHostBindPredicateMatchesEngineBinding(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	var _ index.Host = h
	raw, err := parser.Parse("CREATE INDEX pi ON t(k) WHERE NOT active AND k > 1")
	if err != nil {
		t.Fatal(err)
	}
	predicate := raw[0].(*parser.CreateIndex).Predicate
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	wantTransform, err := tablecmds.TransformIndexPredicate(f.e, h.Op, sqltype.InputEnvironment{}, auth, f.parent, predicate)
	if err != nil {
		t.Fatal(err)
	}
	want, err := tablecmds.CompleteIndexPredicate(h.Op, auth, f.parent, wantTransform)
	if err != nil {
		t.Fatal(err)
	}
	gotTransform, err := h.TransformIndexPredicate(auth, f.parent, predicate, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.CompleteIndexPredicate(auth, f.parent, gotTransform)
	if err != nil {
		t.Fatal(err)
	}
	if want == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("host predicate %#v, engine predicate %#v", got, want)
	}
}

func TestIndexHostLookupCollationKeepsUnsupportedDiagnostic(t *testing.T) {
	h, _, f := indexHostFixture(t)
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	_, err := h.LookupCollation(ddlHostName("ucs_basic"))
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "collation is unsupported" {
		t.Fatalf("LookupCollation(ucs_basic) error %v, want 0A000 collation is unsupported", err)
	}
	if id, err := h.LookupCollation(ddlHostName("C")); err != nil || id == 0 {
		t.Fatalf(`LookupCollation("C") = %d, %v`, id, err)
	}
}

type partialIndexFixture struct {
	e             *core.Engine
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

func ddlHostName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return engine.New().CatalogRead() })

func TestIndexHostTransformCompletionOwnsAnalysis(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	raw := parser.ColumnExpr{Name: ddlHostName("k")}
	transformed, err := h.TransformIndexExpression(auth, f.parent, raw, nil, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	slot := f.parent.Index["k"]
	column, ok := transformed.Definition.(plan.ColumnExpr)
	if !ok || column.Slot != slot || !slices.Contains(transformed.Uses, slot) {
		t.Fatal("transformation lost its actual column identity/use", transformed.Definition, transformed.Uses)
	}
	uses := slices.Clone(transformed.Uses)
	// Completion consumes analyzed facts, not a second binder over source.
	// A now-invalid raw name may affect the label, never the bound column.
	raw.Name = ddlHostName("missing_after_transform")
	key, label, err := h.CompleteIndexExpression(auth, f.parent, raw, transformed)
	if err != nil {
		t.Fatal("completion rebound the changed source", err)
	}
	if key == nil || label != "missing_after_transform" || !reflect.DeepEqual(key.Definition, transformed.Definition) ||
		key.Execution.ResultUsage().Type.ID() != f.parent.Columns[slot].Usage.Type.ID() ||
		!slices.Equal(transformed.Uses, uses) {
		t.Fatal("completion lost/mutated retained definition, type or column-use facts")
	}
}

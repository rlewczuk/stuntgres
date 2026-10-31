package roles_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestInsertConflictACLCurrentManifest(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	admin, done := enginetest.RoleOperation(t, f.e, "postgres")
	defer done()
	if _, err := f.e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "conflict_reader"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, doneReader := enginetest.RoleOperation(t, f.e, "conflict_reader")
	defer doneReader()
	role, _ := f.e.Roles().Lookup("conflict_reader")
	owner, _ := f.e.Roles().Lookup("postgres")
	f.parent.Owner = owner.Ref
	f.index.Owner = owner.Ref
	ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address())
	acl := func(privileges authorization.Privileges) authorization.ACLValue {
		return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: privileges}}}
	}
	if err := enginetest.SetObjectACL(f.e, context.Background(), ref, acl(authorization.InsertPrivilege)); err != nil {
		t.Fatal(err)
	}
	b := dml.BindingForConflict(ref, f.parent, dml.NewInsertConflictState(&analyze.InsertConflict{Targets: []int{0, 1}, Reads: []int{0, 1}}))
	check := func(want string) {
		t.Helper()
		f.e.Mu.Lock()
		auth, err := f.e.AuthorizationLocked(reader)
		f.e.Mu.Unlock()
		if err == nil {
			err = analyze.CheckBinding(auth, b, &parser.Insert{})
		}
		if want == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != want {
			t.Fatal(want, err)
		}
	}
	check("42501")
	column := ref
	column.Address.SubID = 10
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("42501")
	column.Address.SubID = 20
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("")
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	check("42501")
	// Targetless DO NOTHING has no hidden selected-index SELECT manifest.
	b.Insert.Conflict.Reads = nil
	check("")
}

func insertConflictUniqueFixture(t testing.TB) partialIndexFixture {
	t.Helper()
	f := newPartialIndexFixture(t)
	owner, ok := f.e.Roles().Lookup("postgres")
	if !ok {
		t.Fatal("fixture bootstrap principal is missing")
	}
	f.parent.Owner = owner.Ref
	f.index.Owner = owner.Ref
	definition := *f.index.SQLIndex
	definition.Unique = true
	f.index.SQLIndex = &definition
	f.store.Versions = f.store.Versions[:1]
	f.store.Versions[0].Born = 1
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	f.e.Storage.InstallIndexRootLocked(f.index.Address(), root)
	f.e.Mu.Unlock()
	return f
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
	parent.TableMethod = 2
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

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return engine.New().CatalogRead() })

package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Metadata is constructed directly: these units exercise payloads, frames and
// write ownership rather than duplicating the public SQL scenarios.
func triggerUnitFixture(t *testing.T, source string) (*Engine, *StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	return triggerUnitFixtureContext(t, context.Background(), source)
}

func triggerUnitFixtureContext(t *testing.T, ctx context.Context, source string) (*Engine, *StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	e := New()
	read := e.CatalogRead()
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	owner := e.Roles().LookupRef("postgres")
	integer, _ := read.LookupType(sqltype.Int4)
	shape, err := sqltype.Composite(9500002, "trigger_unit_table", "public.trigger_unit_table", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9500001}, ObjectNamespace: namespace, ObjectName: "trigger_unit_table", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: owner, RowType: shape.ID(), Attributes: []int32{1, 2}, Index: map[string]int{"id": 0, "v": 1}, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}, {Name: "v", Usage: integer.Usage(), Nullable: true}}}
	relation.ColumnMeta.HighWater = 2
	rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: namespace, ObjectRevision: 1, Owner: owner, OwningRelation: relation.Address()}
	e.mu.Lock()
	next := catalog.CloneState(e.catalog.State)
	next.SetDescriptor(relation.Address(), relation)
	next.BindName(catalog.DescriptorNameKey(relation), relation.Address())
	next.SetDescriptor(rowtype.Address(), rowtype)
	next.BindName(catalog.DescriptorNameKey(rowtype), rowtype.Address())
	e.catalog.PublishLocked(next)
	e.storage.Relations[relation.Address()] = &storage.Relation{Generation: 1}
	e.mu.Unlock()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Finish(nil) })
	trigger, _ := e.CatalogRead().LookupType(2279)
	d := triggerUnitProcedure(t, e, s, "trigger_unit_fn", 9500003, trigger, true, source)
	return e, s, e.CatalogRead(), relation, d
}

func triggerUnitDefinition(t *testing.T, read catalog.Read, relation *catalog.Relation, d *catalog.Procedure, name string, event stored.TriggerEvent) stored.Trigger {
	t.Helper()
	p := stored.TriggerParts{OID: 9500010, Name: name, Relation: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Procedure: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Events: event, References: []ids.ObjectAddress{d.Address()}}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func triggerUnitInstall(t *testing.T, e *Engine, relation *catalog.Relation, definitions ...stored.Trigger) *catalog.Relation {
	t.Helper()
	copy := *relation
	copy.Triggers = stored.TriggersPayload(definitions)
	e.mu.Lock()
	next := catalog.CloneState(e.catalog.State)
	next.SetDescriptor(copy.Address(), &copy)
	e.catalog.PublishLocked(next)
	e.mu.Unlock()
	return &copy
}

func triggerUnitInt(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func triggerUnitProcedure(t *testing.T, e *Engine, s *StatementScope, name string, oid ids.OID, result *sqltype.Descriptor, trigger bool, source string) *catalog.Procedure {
	t.Helper()
	read := e.CatalogRead()
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectName: name, ObjectRevision: 1, Owner: e.Roles().LookupRef("postgres"), Language: language.Address(), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.ID()), Volatility: expr.Volatile, SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Trigger: trigger, Name: name, Source: source, Result: result.Usage(), PLSyntax: raw}}
	if err := d.SQL.SetFormals(nil); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil {
		err = e.compilePLRoutineLocked(s.op, auth, d)
		s.routineAuth = auth
	}
	if err == nil {
		next := catalog.CloneState(e.catalog.State)
		// Direct fixture publication must maintain the same overload-candidate
		// index as catalog candidate application, not only exact name lookup.
		index := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
		index.Replace(next.Get(d.Address()), d)
		next.SetProcedureNames(index.Index)
		next.SetDescriptor(d.Address(), d)
		next.BindName(catalog.DescriptorNameKey(d), d.Address())
		e.catalog.PublishLocked(next)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

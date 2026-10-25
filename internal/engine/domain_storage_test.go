package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func domainUnitTable(t *testing.T, e *Engine, op *session.Operation, q string) *catalog.Relation {
	t.Helper()
	raw := statement(t, q).(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	if err == nil {
		err = e.SchemaTableLocked(op, auth, stage, raw, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text})
	if len(addresses) != 1 {
		t.Fatal(addresses)
	}
	return view.MustRelation(addresses[0])
}

func TestDomainCascadeStableAttributesAndRowGenerations(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.chain AS public.a"))
	a := domainUnitType(t, e.CatalogRead(), "a")
	chain := domainUnitType(t, e.CatalogRead(), "chain")
	table := domainUnitTable(t, e, op, "CREATE TABLE public.t(keep integer,first public.a,second public.chain,tail integer)")
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	keep, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := sqltype.Integer(integer, 9)
	if err != nil {
		t.Fatal(err)
	}
	value, err := a.SQLType.Input(context.Background(), "7", -1)
	if err != nil {
		t.Fatal(err)
	}
	source := &storage.Relation{Generation: 3, Versions: []storage.RowVersion{{Row: 42, Version: 99, Born: 3, Values: []sqltype.Value{keep, value, sqltype.Null(chain.SQLType), tail}}}}
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), source)
	e.Visibility.SetCommitSeqLocked(3)
	e.Mu.Unlock()
	retained := e.CatalogRead()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	if err = scope.Gate(statement(t, "DROP DOMAIN public.a CASCADE")); err != nil {
		t.Fatal(err)
	}
	restrict := domainUnitStage(t, e, op, "DROP DOMAIN public.a RESTRICT")
	before := e.CatalogRead().State()
	err = scope.stageDomainDependents(restrict, parser.DropRestrict)
	domainUnitDiagnostic(t, err, "2BP01", "cannot drop type a because other objects depend on it")
	if e.CatalogRead().State() != before || e.Storage.Relations[table.Address()] != source {
		t.Fatal("RESTRICT changed roots")
	}
	cascade := domainUnitStage(t, e, op, "DROP DOMAIN public.a CASCADE")
	if err = scope.stageDomainDependents(cascade, parser.DropCascade); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, cascade)
	current := e.CatalogRead()
	next := current.MustRelation(table.Address())
	store := e.Storage.Relations[table.Address()]
	if _, ok := current.Descriptor(a.Address()); ok {
		t.Fatal("root domain retained")
	}
	if _, ok := current.Descriptor(chain.Address()); ok {
		t.Fatal("chain domain retained")
	}
	if len(next.Columns) != 2 || next.Columns[0].Name != "keep" || next.Columns[1].Name != "tail" || next.Attributes[0] != 1 || next.Attributes[1] != 4 || next.Revision() != table.Revision()+1 || store == source || store.Generation != 4 || store.Ends != source.Ends {
		t.Fatal(next, store)
	}
	row := store.Versions[0]
	if row.Row != 42 || row.Version != 99 || row.Born != 3 || len(row.Values) != 2 || !sqltype.SameDatum(row.Values[0], keep) || !sqltype.SameDatum(row.Values[1], tail) {
		t.Fatal(row)
	}
	if retained.Get(table.Address()) != table || len(table.Columns) != 4 || len(source.Versions[0].Values) != 4 || source.Versions[0].Values[1].Type() != a.SQLType {
		t.Fatal("immutable old descriptor/storage altered")
	}
	if err = stored.ValidateTypeProviders(current, next); err != nil {
		t.Fatal("dangling scalar provider", err)
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	recreated := domainUnitType(t, e.CatalogRead(), "a")
	if recreated.Address() == a.Address() || recreated.SQLType == a.SQLType {
		t.Fatal("recreated logical identity reused")
	}
}

func TestDomainExplicitCatalogRemapDetachesPayloadAndBase(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS numeric(5,2)"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.chain AS public.a"))
	a := domainUnitType(t, e.CatalogRead(), "a")
	chain := domainUnitType(t, e.CatalogRead(), "chain")
	table := domainUnitTable(t, e, op, "CREATE TABLE public.t(v public.chain)")
	value, err := chain.SQLType.Input(context.Background(), "12.345", -1)
	if err != nil {
		t.Fatal(err)
	}
	original := &storage.Relation{Generation: 2, Versions: []storage.RowVersion{{Row: 1, Version: 2, Born: 1, Values: []sqltype.Value{value}}}}
	relations := map[ids.ObjectAddress]*storage.Relation{table.Address(): original}
	next := catalog.CloneState(e.CatalogRead().State())
	physical, _ := New().CatalogRead().LookupType(sqltype.Numeric)
	builtin := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Numeric)}
	next.SetDescriptor(builtin, &catalog.Type{SQLType: physical})
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{a.Address(): {ClassID: ids.TypeClassID, ObjectID: 60001}, chain.Address(): {ClassID: ids.TypeClassID, ObjectID: 60002}}
	changed, err := typedef.ReownDomainTypes(next, mapping, false, routine.RangeTypeRebinder(next, mapping))
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if err = stored.ReownScalarTypes(next, relations, mapping); err != nil {
		t.Fatal(err)
	}
	imported := next.MustType(chain.Address())
	if imported.SQLType.ID() != 60002 || imported.SQLType.DomainBase().Type.ID() != 60001 || imported.SQLType.BaseType() != physical || imported.SQLType.Usage().BaseUsage().Typmod != 327686 {
		t.Fatal(imported)
	}
	rebound := relations[table.Address()]
	if rebound == original || rebound.Versions[0].Values[0].Type() != imported.SQLType || next.MustRelation(table.Address()).Columns[0].Usage.Type != imported.SQLType {
		t.Fatal("remap did not detach/retag", rebound)
	}
	text, err := imported.SQLType.Output(context.Background(), rebound.Versions[0].Values[0])
	if err != nil || text != "12.35" {
		t.Fatal(text, err)
	}
	if original.Versions[0].Values[0].Type() != chain.SQLType || table.Columns[0].Usage.Type != chain.SQLType || a.SQLType.BaseType() == physical {
		t.Fatal("source generation mutated")
	}
	if _, err = value.Retag(imported.SQLType); err == nil {
		t.Fatal("ordinary Retag remapped logical ID")
	}
}

func TestDomainDefinitionIdentityDependencyOrder(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	table := domainUnitTable(t, e, op, "CREATE TABLE public.t(keep integer,first integer DEFAULT 7::public.a,second integer DEFAULT 8::public.a,CHECK(keep>0::public.a))")
	first := table.ColumnMeta.Defaults[table.Attributes[1]].OID
	second := table.ColumnMeta.Defaults[table.Attributes[2]].OID
	check := table.Constraints.Check[0].OID
	if first == 0 || first >= second || second >= check {
		t.Fatal("definition allocation does not follow attribute defaults then CHECK", first, second, check)
	}
	stage := domainUnitStage(t, e, op, "DROP DOMAIN public.a RESTRICT")
	dependencies, err := DomainDependencyClosure(context.Background(), stage, stage.DomainTargets)
	if err != nil {
		t.Fatal(err)
	}
	domain := domainUnitType(t, e.CatalogRead(), "a")
	array, ok := e.CatalogRead().LookupArray(domain.SQLType)
	if !ok || len(dependencies) != 4 || !dependencies[0].Automatic || dependencies[0].Address.ObjectID != ids.OID(array.ID()) {
		t.Fatal("missing automatic array lifetime dependency", dependencies)
	}
	dependencies = dependencies[1:]
	if len(dependencies) != 3 || dependencies[0].Address.ObjectID != first || dependencies[1].Address.ObjectID != second || dependencies[2].Address.ObjectID != check {
		t.Fatal("dependency closure lost allocated definition chronology", dependencies)
	}
}

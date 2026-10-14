package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func domainUnitTable(t *testing.T, e *Engine, op *Operation, q string) *relationDescriptor {
	t.Helper()
	raw := statement(t, q).(*parser.CreateTable)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	stage := e.BeginCatalogStage(auth.catalog)
	if err == nil {
		err = e.schemaTableLocked(op, auth, stage, raw, ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, auth.state.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	addresses := view.Lookup(NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text})
	if len(addresses) != 1 {
		t.Fatal(addresses)
	}
	return view.state.descriptors[addresses[0]].(*relationDescriptor)
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
	value, err := a.typ.Input(context.Background(), "7", -1)
	if err != nil {
		t.Fatal(err)
	}
	source := &relationStorage{generation: 3, versions: []rowVersion{{row: 42, version: 99, born: 3, values: []sqltype.Value{keep, value, sqltype.Null(chain.typ), tail}}}}
	e.mu.Lock()
	e.storage[table.address] = source
	e.commitSeq = 3
	e.mu.Unlock()
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
	if err = scope.gate(statement(t, "DROP DOMAIN public.a CASCADE")); err != nil {
		t.Fatal(err)
	}
	restrict := domainUnitStage(t, e, op, "DROP DOMAIN public.a RESTRICT")
	before := e.CatalogRead().state
	err = scope.stageDomainDependents(restrict, parser.DropRestrict)
	domainUnitDiagnostic(t, err, "2BP01", "cannot drop type a because other objects depend on it")
	if e.CatalogRead().state != before || e.storage[table.address] != source {
		t.Fatal("RESTRICT changed roots")
	}
	cascade := domainUnitStage(t, e, op, "DROP DOMAIN public.a CASCADE")
	if err = scope.stageDomainDependents(cascade, parser.DropCascade); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, cascade)
	current := e.CatalogRead()
	next := current.state.descriptors[table.address].(*relationDescriptor)
	store := e.storage[table.address]
	if _, ok := current.Descriptor(a.Address()); ok {
		t.Fatal("root domain retained")
	}
	if _, ok := current.Descriptor(chain.Address()); ok {
		t.Fatal("chain domain retained")
	}
	if len(next.columns) != 2 || next.columns[0].Name != "keep" || next.columns[1].Name != "tail" || next.attributes[0] != 1 || next.attributes[1] != 4 || next.revision != table.revision+1 || store == source || store.generation != 4 || store.ends != source.ends {
		t.Fatal(next, store)
	}
	row := store.versions[0]
	if row.row != 42 || row.version != 99 || row.born != 3 || len(row.values) != 2 || !sqltype.SameDatum(row.values[0], keep) || !sqltype.SameDatum(row.values[1], tail) {
		t.Fatal(row)
	}
	if retained.state.descriptors[table.address] != table || len(table.columns) != 4 || len(source.versions[0].values) != 4 || source.versions[0].values[1].Type() != a.typ {
		t.Fatal("immutable old descriptor/storage altered")
	}
	if err = validateTypeProviders(current, next); err != nil {
		t.Fatal("dangling scalar provider", err)
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	recreated := domainUnitType(t, e.CatalogRead(), "a")
	if recreated.Address() == a.Address() || recreated.typ == a.typ {
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
	value, err := chain.typ.Input(context.Background(), "12.345", -1)
	if err != nil {
		t.Fatal(err)
	}
	original := &relationStorage{generation: 2, versions: []rowVersion{{row: 1, version: 2, born: 1, values: []sqltype.Value{value}}}}
	storage := map[ObjectAddress]*relationStorage{table.address: original}
	next := cloneCatalog(e.CatalogRead().state)
	physical, _ := New().CatalogRead().LookupType(sqltype.Numeric)
	builtin := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(sqltype.Numeric)}
	next.descriptors[builtin] = &typeDescriptor{typ: physical}
	mapping := map[ObjectAddress]ObjectAddress{a.Address(): {ClassID: TypeClassID, ObjectID: 60001}, chain.Address(): {ClassID: TypeClassID, ObjectID: 60002}}
	changed, err := reownDomainTypes(next, mapping, false)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if err = reownCatalogScalarTypes(next, storage, mapping); err != nil {
		t.Fatal(err)
	}
	imported := next.descriptors[chain.Address()].(*typeDescriptor)
	if imported.typ.ID() != 60002 || imported.typ.DomainBase().Type.ID() != 60001 || imported.typ.BaseType() != physical || imported.typ.Usage().BaseUsage().Typmod != 327686 {
		t.Fatal(imported)
	}
	rebound := storage[table.address]
	if rebound == original || rebound.versions[0].values[0].Type() != imported.typ || next.descriptors[table.address].(*relationDescriptor).columns[0].Usage.Type != imported.typ {
		t.Fatal("remap did not detach/retag", rebound)
	}
	text, err := imported.typ.Output(context.Background(), rebound.versions[0].values[0])
	if err != nil || text != "12.35" {
		t.Fatal(text, err)
	}
	if original.versions[0].values[0].Type() != chain.typ || table.columns[0].Usage.Type != chain.typ || a.typ.BaseType() == physical {
		t.Fatal("source generation mutated")
	}
	if _, err = value.Retag(imported.typ); err == nil {
		t.Fatal("ordinary Retag remapped logical ID")
	}
}

func TestDomainDefinitionIdentityDependencyOrder(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	table := domainUnitTable(t, e, op, "CREATE TABLE public.t(keep integer,first integer DEFAULT 7::public.a,second integer DEFAULT 8::public.a,CHECK(keep>0::public.a))")
	first := table.defaults[table.attributes[1]].OID
	second := table.defaults[table.attributes[2]].OID
	check := table.checkConstraints[0].OID
	if first == 0 || first >= second || second >= check {
		t.Fatal("definition allocation does not follow attribute defaults then CHECK", first, second, check)
	}
	stage := domainUnitStage(t, e, op, "DROP DOMAIN public.a RESTRICT")
	dependencies, err := domainDependencyClosure(context.Background(), stage, stage.domainTargets)
	if err != nil {
		t.Fatal(err)
	}
	domain := domainUnitType(t, e.CatalogRead(), "a")
	array, ok := e.CatalogRead().LookupArray(domain.typ)
	if !ok || len(dependencies) != 4 || !dependencies[0].automatic || dependencies[0].address.ObjectID != OID(array.ID()) {
		t.Fatal("missing automatic array lifetime dependency", dependencies)
	}
	dependencies = dependencies[1:]
	if len(dependencies) != 3 || dependencies[0].address.ObjectID != first || dependencies[1].address.ObjectID != second || dependencies[2].address.ObjectID != check {
		t.Fatal("dependency closure lost allocated definition chronology", dependencies)
	}
}

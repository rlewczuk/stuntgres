package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestArrayCatalogAssociationPublicationAndOrigin(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	view := e.CatalogRead()
	for _, descriptor := range view.state.descriptors {
		if typ, ok := descriptor.(*typeDescriptor); ok {
			if err := (typeProvider{}).Validate(view, typ); err != nil {
				t.Fatal(err)
			}
			if typ.array != 0 {
				array, ok := view.LookupArray(typ.typ)
				if !ok || array.Element() != typ.typ || array.ID() != typ.array {
					t.Fatal("builtin reverse association", typ)
				}
			}
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public._collision AS TEXT"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.collision AS INT"))
	domain := domainUnitType(t, e.CatalogRead(), "collision")
	array, ok := e.CatalogRead().LookupArray(domain.typ)
	if !ok || array.Name() != "_collision_1" || array.DatabaseName() != "_COLLISION_1" || array.Element() != domain.typ || array.ID() == domain.typ.ID() {
		t.Fatal("collision association", array)
	}
	associated := e.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(array.ID())}].(*typeDescriptor)
	if associated.owner != domain.owner || associated.Namespace() != domain.Namespace() || associated.Revision() != domain.Revision() {
		t.Fatal("association ownership", associated, domain)
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.array_origin(id INT,a public.collision[],n NUMERIC(5,2)[])")
	column := table.columns[1]
	if column.Usage.Type != array || column.Usage.Typmod != -1 || column.OriginTable != uint32(table.address.ObjectID) || column.OriginAttribute != int16(table.attributes[1]) {
		t.Fatal("array origin", column, table)
	}
	if p, s, known := table.columns[2].Usage.DecimalSize(); p != 5 || s != 2 || !known {
		t.Fatal("element metadata", p, s, known)
	}
	// Both halves of an uncommitted publication remain invisible.
	before := e.CatalogRead()
	stage := domainUnitStage(t, e, op, "CREATE DOMAIN public.pending AS TEXT")
	if len(stage.descriptors) != 2 || len(before.Lookup(ids.NameKey{ClassID: TypeClassID, Namespace: domain.Namespace(), Name: "pending"})) != 0 || len(before.Lookup(ids.NameKey{ClassID: TypeClassID, Namespace: domain.Namespace(), Name: "_pending"})) != 0 {
		t.Fatal("partial domain publication")
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().state != before.state {
		t.Fatal("discarded association changed catalog")
	}
	// Existing staged names participate in generated-name collision resolution.
	staged := domainUnitStage(t, e, op, "CREATE DOMAIN public._staged AS INT")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		err = e.stageCreateDomainLocked(op, auth, staged, statement(t, "CREATE DOMAIN public.staged AS INT").(*parser.CreateDomain), sqltype.InputEnvironment{})
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, staged)
	stagedDomain := domainUnitType(t, e.CatalogRead(), "staged")
	stagedArray, ok := e.CatalogRead().LookupArray(stagedDomain.typ)
	if !ok || stagedArray.Name() != "_staged_1" {
		t.Fatal("staged collision", stagedArray)
	}
	// A foreign element generation or broken reverse ID cannot publish.
	broken := cloneCatalog(e.CatalogRead().state)
	copied := *domain
	copied.array = sqltype.NameArray
	broken.descriptors[domain.Address()] = &copied
	if err := (typeProvider{}).Validate(CatalogRead{owner: e, state: broken}, associated); err == nil {
		t.Fatal("broken reverse association accepted")
	}
	foreignBase, _ := New().CatalogRead().LookupType(sqltype.Int4)
	foreignDomain, err := sqltype.Domain(domain.typ.ID(), domain.Name(), domain.typ.SQLName(), foreignBase.Usage())
	if err != nil {
		t.Fatal(err)
	}
	foreignArray, err := sqltype.Array(array.ID(), array.Name(), foreignDomain)
	if err != nil {
		t.Fatal(err)
	}
	forged := *associated
	forged.typ = foreignArray
	broken = cloneCatalog(e.state)
	broken.descriptors[associated.Address()] = &forged
	if err := (typeProvider{}).Validate(CatalogRead{owner: e, state: broken}, &forged); err == nil {
		t.Fatal("foreign array element accepted")
	}
}

func TestArrayDomainDependencyClosureAndLifetime(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.di AS INT"))
	domain := domainUnitType(t, e.CatalogRead(), "di")
	array, _ := e.CatalogRead().LookupArray(domain.typ)
	table := domainUnitTable(t, e, op, "CREATE TABLE public.array_depend(keep INT,a public.di[],tail INT)")
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.whole AS public.di[]"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.nested AS public.whole[]"))
	stage := domainUnitStage(t, e, op, "DROP DOMAIN public.di RESTRICT")
	edges, err := domainDependencyClosure(context.Background(), stage, stage.domainTargets)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := domainRestrictError(stage, stage.domainTargets, edges, len(stage.domainTargets), ids.ObjectAddress{}).(*sqlstate.Error)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop type di because other objects depend on it", Detail: "column a of table array_depend depends on type di[]\ntype whole depends on type di[]\ntype nested depends on type whole[]", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	if !reflect.DeepEqual(*diagnostic, want) {
		t.Fatalf("dependency=%#v want %#v", *diagnostic, want)
	}
	e.DiscardCatalogStage(stage)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	raw := statement(t, "DROP DOMAIN public.di CASCADE")
	if err = scope.gate(raw); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	stage, err = e.stageSchemaLocked(op, raw, scope.input)
	e.mu.Unlock()
	if err == nil {
		err = scope.stageDomainDependents(stage, parser.DropCascade)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	next := e.CatalogRead()
	survivor := next.state.descriptors[table.address].(*relationDescriptor)
	if len(survivor.columns) != 2 || survivor.columns[0].Name != "keep" || survivor.columns[1].Name != "tail" || survivor.attributes[0] != table.attributes[0] || survivor.attributes[1] != table.attributes[2] {
		t.Fatal("containing table/stable attributes lost", survivor)
	}
	for _, id := range []sqltype.ID{domain.typ.ID(), array.ID()} {
		if _, ok := next.LookupType(id); ok {
			t.Fatal("orphan domain association", id)
		}
	}
	for _, name := range []string{"whole", "nested", "_whole", "_nested"} {
		if len(next.Lookup(ids.NameKey{ClassID: TypeClassID, Namespace: domain.Namespace(), Name: name})) != 0 {
			t.Fatal("transitive type survived", name)
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.di AS INT"))
	recreated := domainUnitType(t, e.CatalogRead(), "di")
	recreatedArray, ok := e.CatalogRead().LookupArray(recreated.typ)
	if !ok || recreated.typ.ID() == domain.typ.ID() || recreatedArray.ID() == array.ID() || recreatedArray.Element() != recreated.typ {
		t.Fatal("recreated association", recreated, recreatedArray)
	}
}

func TestArrayRecursiveExplicitIdentityRemap(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	ctx := context.Background()
	for _, q := range []string{"CREATE DOMAIN public.rc AS REGCLASS", "CREATE DOMAIN public.whole AS public.rc[]", "CREATE DOMAIN public.nested AS public.whole[]"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	rc, whole, nested := domainUnitType(t, e.CatalogRead(), "rc"), domainUnitType(t, e.CatalogRead(), "whole"), domainUnitType(t, e.CatalogRead(), "nested")
	rcArray, _ := e.CatalogRead().LookupArray(rc.typ)
	wholeArray, _ := e.CatalogRead().LookupArray(whole.typ)
	nestedArray, _ := e.CatalogRead().LookupArray(nested.typ)
	referent := domainUnitTable(t, e, op, "CREATE TABLE public.array_target(id INT)")
	table := domainUnitTable(t, e, op, "CREATE TABLE public.array_recursive(a public.nested[])")
	regclass, _ := e.CatalogRead().LookupType(sqltype.Regclass)
	rcValue, err := rc.typ.Wrap(sqltype.String(regclass, strconv.FormatUint(uint64(referent.address.ObjectID), 10)))
	if err != nil {
		t.Fatal(err)
	}
	inner, err := sqltype.ArrayValue(rcArray, []sqltype.ArrayDimension{{Lower: 0, Length: 2}}, []sqltype.Value{rcValue, sqltype.Null(rc.typ)})
	if err != nil {
		t.Fatal(err)
	}
	wholeValue, err := whole.typ.Wrap(inner)
	if err != nil {
		t.Fatal(err)
	}
	middle, err := sqltype.ArrayValue(wholeArray, []sqltype.ArrayDimension{{Lower: -2, Length: 1}}, []sqltype.Value{wholeValue})
	if err != nil {
		t.Fatal(err)
	}
	nestedValue, err := nested.typ.Wrap(middle)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.ArrayValue(nestedArray, []sqltype.ArrayDimension{{Lower: 3, Length: 2}}, []sqltype.Value{nestedValue, sqltype.Null(nested.typ)})
	if err != nil {
		t.Fatal(err)
	}
	original := &relationStorage{generation: 2, versions: []rowVersion{{row: 42, version: 99, born: 1, values: []sqltype.Value{value}}}}
	storage := map[ids.ObjectAddress]*relationStorage{table.address: original}
	next := cloneCatalog(e.state)
	fresh := New()
	regclassAddress := ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}
	// Replace only the builtin scalar generation, retaining this catalog's
	// principal authority; a foreign descriptor would invalidate its array owner.
	reboundRegclass := *next.descriptors[regclassAddress].(*typeDescriptor)
	reboundRegclass.typ = fresh.state.descriptors[regclassAddress].(*typeDescriptor).typ
	next.descriptors[regclassAddress] = &reboundRegclass
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{referent.address: {ClassID: RelationClassID, ObjectID: 60021}}
	for i, typ := range []*sqltype.Descriptor{rc.typ, rcArray, whole.typ, wholeArray, nested.typ, nestedArray} {
		mapping[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(typ.ID())}] = ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(60001 + i)}
	}
	changed, err := reownDomainTypes(next, mapping, false)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if err := reownCatalogScalarTypes(next, storage, mapping); err != nil {
		t.Fatal(err)
	}
	imported := storage[table.address].versions[0].values[0]
	expected := next.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(nestedArray.ID())}].(*typeDescriptor).typ
	if imported.Type() != expected || imported.ArrayElement(0).Type().ID() != 60005 || imported.ArrayElement(1).Type().ID() != 60005 || imported.ArrayElement(0).ArrayElement(0).Type().ID() != 60003 || imported.ArrayElement(0).ArrayElement(0).ArrayElement(0).Type().ID() != 60001 {
		t.Fatal("nested element generations", imported)
	}
	physical, err := imported.ArrayElement(0).ArrayElement(0).ArrayElement(0).BaseValue()
	if err != nil {
		t.Fatal("nested REGCLASS base", err)
	}
	oid, err := physical.Text()
	if err != nil || oid != "60021" {
		t.Fatal("nested REGCLASS identity", oid, err)
	}
	if storage[table.address] == original || original.versions[0].values[0].Type() != nestedArray || table.columns[0].Usage.Type != nestedArray || value.ArrayElement(0).ArrayElement(0).ArrayElement(0).Type() != rc.typ {
		t.Fatal("source payload/provider mutated")
	}
	descriptors := make(map[ids.ObjectAddress]Descriptor, len(next.descriptors))
	names := make(map[ids.NameKey]ids.ObjectAddress, len(next.names))
	for address, descriptor := range next.descriptors {
		target := address
		if mapped, ok := mapping[address]; ok {
			target = mapped
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && target != address {
			copy := *relation
			copy.address = target
			copy.columns = append([]ColumnDescriptor(nil), relation.columns...)
			for i := range copy.columns {
				copy.columns[i].OriginTable = uint32(target.ObjectID)
			}
			descriptor = &copy
		}
		descriptors[target] = descriptor
		names[descriptorNameKey(descriptor)] = target
	}
	next.descriptors, next.names = descriptors, names
	view := CatalogRead{owner: e, state: next}
	for _, descriptor := range next.descriptors {
		if typ, ok := descriptor.(*typeDescriptor); ok {
			if err := (typeProvider{}).Validate(view, typ); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, id := range []sqltype.ID{60001, 60003, 60005} {
		domain, _ := view.LookupType(id)
		array, ok := view.LookupArray(domain)
		if !ok || array.Element() != domain || array.ID() != id+1 {
			t.Fatal("reminted reverse association", id, array)
		}
	}
	rows := newFinalRows(next.descriptors[table.address].(*relationDescriptor).columns, 1)
	defer rows.Close()
	if err := captureRegclassOutputValue(rows, view, nil, imported); err != nil {
		t.Fatal(err)
	}
	if got, err := rows.FormatRegclass(60021); err != nil || got != "public.array_target" {
		t.Fatal("recursive captured presentation", got, err)
	}
	if _, err := expected.OutputWithEnvironment(ctx, imported, rows.OutputEnvironment()); err != nil {
		t.Fatal("nested output formatter", err)
	}
}

// These are valid catalog-provider component fixtures, not public TEMP-table SQL.
func arrayComponentTemporary(t *testing.T, e *Engine, op *Operation, prototype *relationDescriptor) *relationDescriptor {
	t.Helper()
	e.mu.Lock()
	nsOID, err := e.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	tableOID, err := e.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	revision, err := e.revisionLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ns := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: nsOID}, name: "pg_temp_array_component", revision: revision, owner: op.RoleState().CurrentUser, temporary: op.session.token}
	table := *prototype
	table.address = ids.ObjectAddress{ClassID: RelationClassID, ObjectID: tableOID}
	table.namespace = ns.address
	table.name = "array_component_temp"
	table.revision = revision
	table.rowType = 0
	table.columns = append([]ColumnDescriptor(nil), prototype.columns...)
	for i := range table.columns {
		table.columns[i].OriginTable = uint32(tableOID)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(ns); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.stageRowTypeLocked(stage, &table)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&table); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	e.mu.Lock()
	op.session.mu.Lock()
	op.session.temporaryNamespace = ns.address
	op.session.mu.Unlock()
	e.mu.Unlock()
	return &table
}

func TestArrayRetainedTemporaryBuiltinClosureComponent(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	ctx := context.Background()
	prototype := domainUnitTable(t, e, op, "CREATE TABLE public.array_component(i INT[],t TEXT[])")
	table := arrayComponentTemporary(t, e, op, prototype)
	integer, text := table.columns[0].Usage.Type, table.columns[1].Usage.Type
	i, err := integer.Input(ctx, "[-2:-1]={5,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	v, err := text.Input(ctx, `{NULL,"NULL","a,b"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	store := &relationStorage{generation: 3, versions: []rowVersion{{row: 42, version: 99, born: 1, values: []sqltype.Value{i, v}}}}
	e.mu.Lock()
	e.storage[table.address] = store
	e.commitSeq = 1
	e.mu.Unlock()
	held := newFinalRows(table.columns, 1)
	held.values = store.versions[0].values
	defer held.Close()
	replacement := New()
	foreign, _ := replacement.CatalogRead().LookupType(1007)
	if foreign == integer {
		t.Fatal("fixture lacks distinct generation")
	}
	actor, finish := roleOperation(t, replacement, "postgres")
	defer finish()
	importedSource := domainUnitTable(t, replacement, actor, "CREATE TABLE public.imported_arrays(i INT[],t TEXT[])")
	sourceInt, err := importedSource.columns[0].Usage.Type.Input(ctx, "[-2:-1]={5,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	sourceText, err := importedSource.columns[1].Usage.Type.Input(ctx, `{NULL,"NULL","a,b"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	sourceStore := &relationStorage{generation: 2, versions: []rowVersion{{row: 4, version: 7, born: 1, values: []sqltype.Value{sourceInt, sourceText}}}}
	replacement.mu.Lock()
	replacement.storage[importedSource.address] = sourceStore
	replacement.commitSeq = 1
	replacement.mu.Unlock()
	importedTypes := make(map[ids.ObjectAddress]*typeDescriptor)
	for address, descriptor := range replacement.CatalogRead().state.descriptors {
		if typ, ok := descriptor.(*typeDescriptor); ok && typ.typ.NamespaceName() != "" && typ.owningRelation == (ids.ObjectAddress{}) {
			importedTypes[address] = typ
		}
	}
	if _, err := e.ReplaceCatalog(ctx, replacement.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	if current.state.descriptors[table.address] != table || e.storage[table.address] != store || held.Schema()[0].Usage.Type != integer {
		t.Fatal("retained TEMP identity changed")
	}
	if row := store.versions[0]; row.row != 42 || row.version != 99 || row.born != 1 || store.generation != 3 {
		t.Fatal("retained TEMP row lifetime changed", row, store.generation)
	}
	for _, typ := range []*sqltype.Descriptor{integer, text} {
		actual, ok := current.LookupType(typ.ID())
		element, elementOK := current.LookupType(typ.Element().ID())
		associated, arrayOK := current.LookupArray(element)
		if !ok || !elementOK || !arrayOK || actual != typ || element != typ.Element() || associated != typ {
			t.Fatal("retained builtin closure", typ)
		}
	}
	for _, descriptor := range current.state.descriptors {
		if err := e.providers[descriptor.Address().ClassID].Validate(current, descriptor); err != nil {
			t.Fatal("invalid retained provider", descriptor.Address(), descriptor.Name(), err)
		}
	}
	for address, source := range importedTypes {
		actual := current.state.descriptors[address].(*typeDescriptor)
		if actual.Namespace() != source.Namespace() || actual.comment != source.comment || !reflect.DeepEqual(actual.acl, source.acl) {
			t.Fatal("imported type catalog ownership changed", address)
		}
		sourceOwner, sourceErr := replacement.Roles().Descriptor(source.owner)
		targetOwner, targetErr := e.Roles().Descriptor(actual.owner)
		if sourceErr != nil || targetErr != nil || sourceOwner.Name != targetOwner.Name {
			t.Fatal("imported type principal ownership did not remap", address, sourceErr, targetErr)
		}
		if err := validateACL(e.Roles(), actual); err != nil {
			t.Fatal("imported type principal closure", address, err)
		}
		if source.typ.IsDomain() {
			base, ok := current.LookupType(source.typ.DomainBase().Type.ID())
			if !ok || actual.typ.DomainBase().Type != base || actual.typ.Concrete() != source.typ.Concrete() || actual.typ.IsUserDefined() {
				t.Fatal("seeded domain closure/admission changed", address)
			}
		}
		if replacement.CatalogRead().state.descriptors[address] != source {
			t.Fatal("source type catalog mutated", address)
		}
	}
	importedAddresses := current.Lookup(ids.NameKey{ClassID: RelationClassID, Namespace: ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "imported_arrays"})
	if len(importedAddresses) != 1 {
		t.Fatal("missing imported persistent table", importedAddresses)
	}
	imported := current.state.descriptors[importedAddresses[0]].(*relationDescriptor)
	importedStore := e.storage[imported.address]
	if importedStore == nil || len(importedStore.versions) != 1 {
		t.Fatal("missing imported persistent storage", importedStore)
	}
	live := importedStore.versions[0].values
	if imported.columns[0].Usage.Type != integer || imported.columns[1].Usage.Type != text || len(live) != 2 || !sqltype.SameDatum(live[0], i) || !sqltype.SameDatum(live[1], v) {
		t.Fatal("persistent rows not reowned against retained builtin closure", imported, live)
	}
	if sourceStore.versions[0].values[0].Type() != foreign || importedSource.columns[0].Usage.Type != foreign || replacement.storage[importedSource.address] != sourceStore {
		t.Fatal("source persistent provider/payload mutated")
	}
	frame := make([]sqltype.Value, 2)
	present, err := held.Next(ctx, frame)
	if err != nil || !present || !sqltype.SameDatum(frame[0], i) || !sqltype.SameDatum(frame[1], v) {
		t.Fatal("held payload changed", frame, err)
	}
}

func TestArrayRetainedTemporaryDomainBoundaryComponent(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	ctx := context.Background()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.array_temp_di AS INT"))
	prototype := domainUnitTable(t, e, op, "CREATE TABLE public.array_component(a public.array_temp_di[])")
	table := arrayComponentTemporary(t, e, op, prototype)
	typ := table.columns[0].Usage.Type
	value, err := typ.Input(ctx, "[0:1]={13,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	store := &relationStorage{generation: 3, versions: []rowVersion{{row: 42, version: 99, born: 1, values: []sqltype.Value{value}}}}
	e.mu.Lock()
	e.storage[table.address] = store
	e.commitSeq = 1
	e.mu.Unlock()
	held := newFinalRows(table.columns, 1)
	held.values = store.versions[0].values
	defer held.Close()
	if _, err := e.ReplaceCatalog(ctx, e.CatalogRead()); err != nil {
		t.Fatal("compatible exact self replacement", err)
	}
	before := e.CatalogRead()
	oldStore := e.storage[table.address]
	frontier := e.nextOID
	revision := e.nextRevision
	unrelated := New()
	actor, finish := roleOperation(t, unrelated, "postgres")
	domainUnitPublish(t, unrelated, domainUnitStage(t, unrelated, actor, "CREATE DOMAIN public.array_temp_di AS INT"))
	finish()
	for _, replace := range []func() error{
		func() error { return e.Reset(ctx) },
		func() error {
			return e.Restore(ctx, strings.NewReader("CREATE DOMAIN public.array_temp_di AS INT; CREATE TABLE public.array_component(a public.array_temp_di[]);"))
		},
		func() error { _, err := e.ReplaceCatalog(ctx, unrelated.CatalogRead()); return err },
	} {
		err := replace()
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) || err.Error() != "stuntgres: catalog replacement with incompatible live temporary user-domain array dependencies is unsupported" {
			t.Fatal("ordinary boundary", err)
		}
		if e.CatalogRead().state != before.state || e.storage[table.address] != oldStore || e.nextOID != frontier || e.nextRevision != revision || before.state.descriptors[table.address] != table || held.Schema()[0].Usage.Type != typ {
			t.Fatal("rejected replacement changed target state")
		}
		domain, _ := before.LookupType(typ.Element().ID())
		association, ok := before.LookupArray(domain)
		if !ok || association != typ {
			t.Fatal("rejected replacement changed association")
		}
	}
	frame := make([]sqltype.Value, 1)
	present, err := held.Next(ctx, frame)
	if err != nil || !present || !sqltype.SameDatum(frame[0], value) {
		t.Fatal("rejected replacement changed held cursor", err)
	}
}

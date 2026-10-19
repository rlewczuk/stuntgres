package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
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
	for _, descriptor := range view.Descriptors() {
		if typ, ok := descriptor.(*catalog.Type); ok {
			if err := (typeProvider{}).Validate(view, typ); err != nil {
				t.Fatal(err)
			}
			if typ.Array != 0 {
				array, ok := view.LookupArray(typ.SQLType)
				if !ok || array.Element() != typ.SQLType || array.ID() != typ.Array {
					t.Fatal("builtin reverse association", typ)
				}
			}
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public._collision AS TEXT"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.collision AS INT"))
	domain := domainUnitType(t, e.CatalogRead(), "collision")
	array, ok := e.CatalogRead().LookupArray(domain.SQLType)
	if !ok || array.Name() != "_collision_1" || array.DatabaseName() != "_COLLISION_1" || array.Element() != domain.SQLType || array.ID() == domain.SQLType.ID() {
		t.Fatal("collision association", array)
	}
	associated := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())})
	if associated.Owner != domain.Owner || associated.Namespace() != domain.Namespace() || associated.Revision() != domain.Revision() {
		t.Fatal("association ownership", associated, domain)
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.array_origin(id INT,a public.collision[],n NUMERIC(5,2)[])")
	column := table.Columns[1]
	if column.Usage.Type != array || column.Usage.Typmod != -1 || column.OriginTable != uint32(table.Address().ObjectID) || column.OriginAttribute != int16(table.Attributes[1]) {
		t.Fatal("array origin", column, table)
	}
	if p, s, known := table.Columns[2].Usage.DecimalSize(); p != 5 || s != 2 || !known {
		t.Fatal("element metadata", p, s, known)
	}
	// Both halves of an uncommitted publication remain invisible.
	before := e.CatalogRead()
	stage := domainUnitStage(t, e, op, "CREATE DOMAIN public.pending AS TEXT")
	if len(stage.Descriptors) != 2 || len(before.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: domain.Namespace(), Name: "pending"})) != 0 || len(before.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: domain.Namespace(), Name: "_pending"})) != 0 {
		t.Fatal("partial domain publication")
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().State() != before.State() {
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
	stagedArray, ok := e.CatalogRead().LookupArray(stagedDomain.SQLType)
	if !ok || stagedArray.Name() != "_staged_1" {
		t.Fatal("staged collision", stagedArray)
	}
	// A foreign element generation or broken reverse ID cannot publish.
	broken := catalog.CloneState(e.CatalogRead().State())
	copied := *domain
	copied.Array = sqltype.NameArray
	broken.SetDescriptor(domain.Address(), &copied)
	if err := (typeProvider{}).Validate(e.catalog.read(broken), associated); err == nil {
		t.Fatal("broken reverse association accepted")
	}
	foreignBase, _ := New().CatalogRead().LookupType(sqltype.Int4)
	foreignDomain, err := sqltype.Domain(domain.SQLType.ID(), domain.Name(), domain.SQLType.SQLName(), foreignBase.Usage())
	if err != nil {
		t.Fatal(err)
	}
	foreignArray, err := sqltype.Array(array.ID(), array.Name(), foreignDomain)
	if err != nil {
		t.Fatal(err)
	}
	forged := *associated
	forged.SQLType = foreignArray
	broken = catalog.CloneState(e.catalog.state)
	broken.SetDescriptor(associated.Address(), &forged)
	if err := (typeProvider{}).Validate(e.catalog.read(broken), &forged); err == nil {
		t.Fatal("foreign array element accepted")
	}
}

func TestArrayDomainDependencyClosureAndLifetime(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.di AS INT"))
	domain := domainUnitType(t, e.CatalogRead(), "di")
	array, _ := e.CatalogRead().LookupArray(domain.SQLType)
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
	survivor := next.MustRelation(table.Address())
	if len(survivor.Columns) != 2 || survivor.Columns[0].Name != "keep" || survivor.Columns[1].Name != "tail" || survivor.Attributes[0] != table.Attributes[0] || survivor.Attributes[1] != table.Attributes[2] {
		t.Fatal("containing table/stable attributes lost", survivor)
	}
	for _, id := range []sqltype.ID{domain.SQLType.ID(), array.ID()} {
		if _, ok := next.LookupType(id); ok {
			t.Fatal("orphan domain association", id)
		}
	}
	for _, name := range []string{"whole", "nested", "_whole", "_nested"} {
		if len(next.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: domain.Namespace(), Name: name})) != 0 {
			t.Fatal("transitive type survived", name)
		}
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.di AS INT"))
	recreated := domainUnitType(t, e.CatalogRead(), "di")
	recreatedArray, ok := e.CatalogRead().LookupArray(recreated.SQLType)
	if !ok || recreated.SQLType.ID() == domain.SQLType.ID() || recreatedArray.ID() == array.ID() || recreatedArray.Element() != recreated.SQLType {
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
	rcArray, _ := e.CatalogRead().LookupArray(rc.SQLType)
	wholeArray, _ := e.CatalogRead().LookupArray(whole.SQLType)
	nestedArray, _ := e.CatalogRead().LookupArray(nested.SQLType)
	referent := domainUnitTable(t, e, op, "CREATE TABLE public.array_target(id INT)")
	table := domainUnitTable(t, e, op, "CREATE TABLE public.array_recursive(a public.nested[])")
	regclass, _ := e.CatalogRead().LookupType(sqltype.Regclass)
	rcValue, err := rc.SQLType.Wrap(sqltype.String(regclass, strconv.FormatUint(uint64(referent.Address().ObjectID), 10)))
	if err != nil {
		t.Fatal(err)
	}
	inner, err := sqltype.ArrayValue(rcArray, []sqltype.ArrayDimension{{Lower: 0, Length: 2}}, []sqltype.Value{rcValue, sqltype.Null(rc.SQLType)})
	if err != nil {
		t.Fatal(err)
	}
	wholeValue, err := whole.SQLType.Wrap(inner)
	if err != nil {
		t.Fatal(err)
	}
	middle, err := sqltype.ArrayValue(wholeArray, []sqltype.ArrayDimension{{Lower: -2, Length: 1}}, []sqltype.Value{wholeValue})
	if err != nil {
		t.Fatal(err)
	}
	nestedValue, err := nested.SQLType.Wrap(middle)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.ArrayValue(nestedArray, []sqltype.ArrayDimension{{Lower: 3, Length: 2}}, []sqltype.Value{nestedValue, sqltype.Null(nested.SQLType)})
	if err != nil {
		t.Fatal(err)
	}
	original := &storage.Relation{Generation: 2, Versions: []storage.RowVersion{{Row: 42, Version: 99, Born: 1, Values: []sqltype.Value{value}}}}
	storage := map[ids.ObjectAddress]*storage.Relation{table.Address(): original}
	next := catalog.CloneState(e.catalog.state)
	fresh := New()
	regclassAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}
	// Replace only the builtin scalar generation, retaining this catalog's
	// principal authority; a foreign descriptor would invalidate its array owner.
	reboundRegclass := *next.MustType(regclassAddress)
	reboundRegclass.SQLType = fresh.catalog.state.MustType(regclassAddress).SQLType
	next.SetDescriptor(regclassAddress, &reboundRegclass)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{referent.Address(): {ClassID: ids.RelationClassID, ObjectID: 60021}}
	for i, typ := range []*sqltype.Descriptor{rc.SQLType, rcArray, whole.SQLType, wholeArray, nested.SQLType, nestedArray} {
		mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(60001 + i)}
	}
	changed, err := reownDomainTypes(next, mapping, false)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if err := reownCatalogScalarTypes(next, storage, mapping); err != nil {
		t.Fatal(err)
	}
	imported := storage[table.Address()].Versions[0].Values[0]
	expected := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(nestedArray.ID())}).SQLType
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
	if storage[table.Address()] == original || original.Versions[0].Values[0].Type() != nestedArray || table.Columns[0].Usage.Type != nestedArray || value.ArrayElement(0).ArrayElement(0).ArrayElement(0).Type() != rc.SQLType {
		t.Fatal("source payload/provider mutated")
	}
	descriptors := make(map[ids.ObjectAddress]catalog.Descriptor, next.DescriptorCount())
	names := make(map[ids.NameKey]ids.ObjectAddress, next.NameCount())
	for address, descriptor := range next.Descriptors() {
		target := address
		if mapped, ok := mapping[address]; ok {
			target = mapped
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && target != address {
			copy := *relation
			copy.ObjectAddress = target
			copy.Columns = append([]catalog.Column(nil), relation.Columns...)
			for i := range copy.Columns {
				copy.Columns[i].OriginTable = uint32(target.ObjectID)
			}
			descriptor = &copy
		}
		descriptors[target] = descriptor
		names[catalog.DescriptorNameKey(descriptor)] = target
	}
	next.ResetMaps(len(descriptors))
	for address, descriptor := range descriptors {
		next.SetDescriptor(address, descriptor)
	}
	for key, address := range names {
		next.BindName(key, address)
	}
	view := e.catalog.read(next)
	for _, descriptor := range next.Descriptors() {
		if typ, ok := descriptor.(*catalog.Type); ok {
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
	rows := newFinalRows(next.MustRelation(table.Address()).Columns, 1)
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
func arrayComponentTemporary(t *testing.T, e *Engine, op *Operation, prototype *catalog.Relation) *catalog.Relation {
	t.Helper()
	e.mu.Lock()
	nsOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	tableOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	revision, err := e.catalog.revisionLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: nsOID}, ObjectName: "pg_temp_array_component", ObjectRevision: revision, Owner: op.RoleState().CurrentUser, Temporary: op.session.token}
	table := *prototype
	table.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tableOID}
	table.ObjectNamespace = ns.Address()
	table.ObjectName = "array_component_temp"
	table.ObjectRevision = revision
	table.RowType = 0
	table.Columns = append([]catalog.Column(nil), prototype.Columns...)
	for i := range table.Columns {
		table.Columns[i].OriginTable = uint32(tableOID)
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
	op.session.temporaryNamespace = ns.Address()
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
	integer, text := table.Columns[0].Usage.Type, table.Columns[1].Usage.Type
	i, err := integer.Input(ctx, "[-2:-1]={5,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	v, err := text.Input(ctx, `{NULL,"NULL","a,b"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Generation: 3, Versions: []storage.RowVersion{{Row: 42, Version: 99, Born: 1, Values: []sqltype.Value{i, v}}}}
	e.mu.Lock()
	e.storage.Relations[table.Address()] = store
	e.commitSeq = 1
	e.mu.Unlock()
	held := newFinalRows(table.Columns, 1)
	held.values = store.Versions[0].Values
	defer held.Close()
	replacement := New()
	foreign, _ := replacement.CatalogRead().LookupType(1007)
	if foreign == integer {
		t.Fatal("fixture lacks distinct generation")
	}
	actor, finish := roleOperation(t, replacement, "postgres")
	defer finish()
	importedSource := domainUnitTable(t, replacement, actor, "CREATE TABLE public.imported_arrays(i INT[],t TEXT[])")
	sourceInt, err := importedSource.Columns[0].Usage.Type.Input(ctx, "[-2:-1]={5,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	sourceText, err := importedSource.Columns[1].Usage.Type.Input(ctx, `{NULL,"NULL","a,b"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	sourceStore := &storage.Relation{Generation: 2, Versions: []storage.RowVersion{{Row: 4, Version: 7, Born: 1, Values: []sqltype.Value{sourceInt, sourceText}}}}
	replacement.mu.Lock()
	replacement.storage.Relations[importedSource.Address()] = sourceStore
	replacement.commitSeq = 1
	replacement.mu.Unlock()
	importedTypes := make(map[ids.ObjectAddress]*catalog.Type)
	for address, descriptor := range replacement.CatalogRead().Descriptors() {
		if typ, ok := descriptor.(*catalog.Type); ok && typ.SQLType.NamespaceName() != "" && typ.OwningRelation == (ids.ObjectAddress{}) {
			importedTypes[address] = typ
		}
	}
	if _, err := e.ReplaceCatalog(ctx, replacement, replacement.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	if current.Get(table.Address()) != table || e.storage.Relations[table.Address()] != store || held.Schema()[0].Usage.Type != integer {
		t.Fatal("retained TEMP identity changed")
	}
	if row := store.Versions[0]; row.Row != 42 || row.Version != 99 || row.Born != 1 || store.Generation != 3 {
		t.Fatal("retained TEMP row lifetime changed", row, store.Generation)
	}
	for _, typ := range []*sqltype.Descriptor{integer, text} {
		actual, ok := current.LookupType(typ.ID())
		element, elementOK := current.LookupType(typ.Element().ID())
		associated, arrayOK := current.LookupArray(element)
		if !ok || !elementOK || !arrayOK || actual != typ || element != typ.Element() || associated != typ {
			t.Fatal("retained builtin closure", typ)
		}
	}
	for _, descriptor := range current.Descriptors() {
		if err := e.catalog.Providers[descriptor.Address().ClassID].Validate(current, descriptor); err != nil {
			t.Fatal("invalid retained provider", descriptor.Address(), descriptor.Name(), err)
		}
	}
	for address, source := range importedTypes {
		actual := current.MustType(address)
		if actual.Namespace() != source.Namespace() || actual.Comment != source.Comment || !reflect.DeepEqual(actual.ACL, source.ACL) {
			t.Fatal("imported type catalog ownership changed", address)
		}
		sourceOwner, sourceErr := replacement.Roles().Descriptor(source.Owner)
		targetOwner, targetErr := e.Roles().Descriptor(actual.Owner)
		if sourceErr != nil || targetErr != nil || sourceOwner.Name != targetOwner.Name {
			t.Fatal("imported type principal ownership did not remap", address, sourceErr, targetErr)
		}
		if err := validateACL(e.Roles(), actual); err != nil {
			t.Fatal("imported type principal closure", address, err)
		}
		if source.SQLType.IsDomain() {
			base, ok := current.LookupType(source.SQLType.DomainBase().Type.ID())
			if !ok || actual.SQLType.DomainBase().Type != base || actual.SQLType.Concrete() != source.SQLType.Concrete() || actual.SQLType.IsUserDefined() {
				t.Fatal("seeded domain closure/admission changed", address)
			}
		}
		if replacement.CatalogRead().Get(address) != source {
			t.Fatal("source type catalog mutated", address)
		}
	}
	importedAddresses := current.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "imported_arrays"})
	if len(importedAddresses) != 1 {
		t.Fatal("missing imported persistent table", importedAddresses)
	}
	imported := current.MustRelation(importedAddresses[0])
	importedStore := e.storage.Relations[imported.Address()]
	if importedStore == nil || len(importedStore.Versions) != 1 {
		t.Fatal("missing imported persistent storage", importedStore)
	}
	live := importedStore.Versions[0].Values
	if imported.Columns[0].Usage.Type != integer || imported.Columns[1].Usage.Type != text || len(live) != 2 || !sqltype.SameDatum(live[0], i) || !sqltype.SameDatum(live[1], v) {
		t.Fatal("persistent rows not reowned against retained builtin closure", imported, live)
	}
	if sourceStore.Versions[0].Values[0].Type() != foreign || importedSource.Columns[0].Usage.Type != foreign || replacement.storage.Relations[importedSource.Address()] != sourceStore {
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
	typ := table.Columns[0].Usage.Type
	value, err := typ.Input(ctx, "[0:1]={13,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Generation: 3, Versions: []storage.RowVersion{{Row: 42, Version: 99, Born: 1, Values: []sqltype.Value{value}}}}
	e.mu.Lock()
	e.storage.Relations[table.Address()] = store
	e.commitSeq = 1
	e.mu.Unlock()
	held := newFinalRows(table.Columns, 1)
	held.values = store.Versions[0].Values
	defer held.Close()
	if _, err := e.ReplaceCatalog(ctx, e, e.CatalogRead()); err != nil {
		t.Fatal("compatible exact self replacement", err)
	}
	before := e.CatalogRead()
	oldStore := e.storage.Relations[table.Address()]
	frontier := e.catalog.nextOID
	revision := e.catalog.nextRevision
	unrelated := New()
	actor, finish := roleOperation(t, unrelated, "postgres")
	domainUnitPublish(t, unrelated, domainUnitStage(t, unrelated, actor, "CREATE DOMAIN public.array_temp_di AS INT"))
	finish()
	for _, replace := range []func() error{
		func() error { return e.Reset(ctx) },
		func() error {
			return e.Restore(ctx, strings.NewReader("CREATE DOMAIN public.array_temp_di AS INT; CREATE TABLE public.array_component(a public.array_temp_di[]);"))
		},
		func() error { _, err := e.ReplaceCatalog(ctx, unrelated, unrelated.CatalogRead()); return err },
	} {
		err := replace()
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) || err.Error() != "stuntgres: catalog replacement with incompatible live temporary user-domain array dependencies is unsupported" {
			t.Fatal("ordinary boundary", err)
		}
		if e.CatalogRead().State() != before.State() || e.storage.Relations[table.Address()] != oldStore || e.catalog.nextOID != frontier || e.catalog.nextRevision != revision || before.Get(table.Address()) != table || held.Schema()[0].Usage.Type != typ {
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

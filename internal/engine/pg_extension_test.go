package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgExtensionUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3079}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_extension")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), a)
}

func pgExtensionUnitShape(t *testing.T, e *Engine) *catalog.Relation {
	t.Helper()
	d, _ := pgExtensionUnitSource(t, e)
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if d.Name() != "pg_extension" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 8 || len(d.Columns) != 8 || d.Owner != owner || owner.ID() != 10 {
		t.Fatal("relation identity", d, owner)
	}
	names := [8]string{"oid", "extname", "extowner", "extnamespace", "extrelocatable", "extversion", "extconfig", "extcondition"}
	typeIDs := [8]sqltype.ID{26, 19, 26, 26, 16, 25, 1028, 1009}
	collations := [8]uint32{0, 950, 0, 0, 0, 950, 0, 950}
	for i, c := range d.Columns {
		owned, ok := e.CatalogRead().LookupType(typeIDs[i])
		if !ok || c.Name != names[i] || c.Usage.Type != owned || c.Usage.Type.ID() != typeIDs[i] || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != collations[i] || c.Nullable != (i >= 6) || !c.NullabilityKnown || c.OriginTable != 3079 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[names[i]] != i {
			t.Fatal("column", i, c)
		}
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Explicit || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Grantor != owner || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	if d.RowType != 10074 {
		t.Fatal("existing native rowtype", d.RowType)
	}
	row := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10074})
	array := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10073})
	ownedRow, ok := e.CatalogRead().LookupType(10074)
	ownedArray, arrayOK := e.CatalogRead().LookupArray(ownedRow)
	if !ok || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10073 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != owner || array.Owner != owner {
		t.Fatal("row/array ownership", row, array)
	}
	return d
}

func TestPGExtensionShapeAndOwnership(t *testing.T) {
	e := New()
	d := pgExtensionUnitShape(t, e)
	foreign, _ := New().CatalogRead().LookupType(sqltype.Oid)
	invalid := *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	invalid.Columns[0].Usage.Type = foreign
	if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("foreign generation accepted")
	}
	class := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows.Anonymous {
		oid, _ := row[0].OID()
		if oid != 3079 {
			continue
		}
		name, _ := row[1].Text()
		ns, _ := row[2].OID()
		kind, _ := row[class.Index["relkind"]].Text()
		count, _ := row[class.Index["relnatts"]].Integer()
		if name != "pg_extension" || ns != 11 || kind != "r" || count != 8 {
			t.Fatal("pg_class projection", row)
		}
		found++
	}
	if found != 1 {
		t.Fatal("pg_class identity count", found)
	}
}

func TestPGExtensionCapture(t *testing.T) {
	e := New()
	d, ref := pgExtensionUnitSource(t, e)
	before, names := maps.Collect(e.catalog.state.Descriptors()), maps.Collect(e.catalog.state.Names())
	old, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), ref, d)
	if err != nil || old == nil {
		t.Fatal("capture", old, err)
	}
	if old.Ref != ref || old.Anonymous == nil || old.Count() != 0 || !maps.Equal(before, maps.Collect(e.catalog.state.Descriptors())) || !maps.Equal(names, maps.Collect(e.catalog.state.Names())) {
		t.Fatal("empty immutable capture", old)
	}
	root := &plan.ProjectNode{QuerySchema: plan.QuerySchema(d.Columns), Child: &plan.SourceNode{QuerySchema: plan.QuerySchema(d.Columns), Ref: ref}}
	for i, c := range d.Columns {
		root.Expressions = append(root.Expressions, plan.ColumnExpr{Info: expr.Info{Usage: c.Usage}, Origin: ref, Scope: 1, Frame: 1, Slot: i})
	}
	executable, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, captureExtensionCatalog)
	if err != nil {
		t.Fatal(err)
	}
	rows, count, err := exec.Finalize(context.Background(), root, executable, nil)
	if err != nil {
		t.Fatal("empty captured source must execute", err)
	}
	if count != 0 || !reflect.DeepEqual(rows.Schema(), d.Columns) {
		t.Fatal("empty result shape/count", rows.Schema(), count)
	}
	var emptyDestination [8]sqltype.Value
	if next, err := rows.Next(context.Background(), emptyDestination[:]); next || err != nil {
		t.Fatal("empty result row", next, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{0, 7, 9} {
		invalid := *d
		invalid.Columns = make([]catalog.Column, width)
		if rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: &invalid, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, captureExtensionCatalog); err == nil || rows != nil {
			t.Fatal("malformed width", width, rows, err)
		}
	}
	foreign := New()
	for i, c := range d.Columns {
		invalid := *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(c.Usage.Type.ID())
		if rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: &invalid, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, captureExtensionCatalog); err == nil || rows != nil {
			t.Fatal("foreign column", i, rows, err)
		}
	}
	state := *e.catalog.state
	state.CloneDescriptors()
	for a := range state.Descriptors() {
		if a.ClassID == ids.RelationClassID {
			state.RemoveDescriptor(a)
		}
	}
	view := e.catalog.read(&state)
	if rows, err := e.captureCatalogSourceLocked(context.Background(), view, e.roles.read(e.catalog.owner(), view, e.roles.published), ref, d); err != nil || rows == nil || rows.Count() != 0 {
		t.Fatal("unrelated inventory dependency", rows, err)
	}
	state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1028})
	if rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: d, Owner: (view).Owner(), Runtime: engineCatalogRuntime{}}, captureExtensionCatalog); err == nil || rows != nil {
		t.Fatal("missing array descriptor", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, captureExtensionCatalog); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", rows, err)
	}
	retained := &indexedStage{Source: old, Columns: d.Columns}
	original := append([]catalog.Column(nil), retained.Schema()...)
	for _, replace := range []func() error{
		func() error { return e.Reset(context.Background()) },
		func() error { _, err := replaceWithFreshCatalog(context.Background(), e); return err },
	} {
		if err := replace(); err != nil {
			t.Fatal(err)
		}
		fresh := pgExtensionUnitShape(t, e)
		for i, c := range fresh.Columns {
			if c.Usage.Type == d.Columns[i].Usage.Type {
				t.Fatal("generation not reminted", i)
			}
		}
		if rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: (e.CatalogRead()).Owner(), Runtime: engineCatalogRuntime{}}, captureExtensionCatalog); err == nil || rows != nil {
			t.Fatal("old source accepted in replacement", rows, err)
		}
		if !reflect.DeepEqual(retained.Schema(), original) || old.Ref != ref || old.Count() != 0 {
			t.Fatal("retained source/schema changed")
		}
	}
	var destination [8]sqltype.Value
	if next, err := retained.Next(context.Background(), destination[:]); next || err != nil {
		t.Fatal("empty retained EOF", next, err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retained.Schema(), original) {
		t.Fatal("schema lost after EOF/Close")
	}
}

func TestPGExtensionCaptureInstalled(t *testing.T) {
	e := New()
	d, ref := pgExtensionUnitSource(t, e)
	empty, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), ref, d)
	if err != nil || empty == nil || empty.Count() != 0 {
		t.Fatal("fresh installed catalog", empty, err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage := extensionUnitStage(t, e, op)
	extensionUnitPublish(t, e, op, stage)
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	ext := e.catalog.state.MustExtension(address)
	d = pgExtensionUnitShape(t, e)
	captured, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), ref, d)
	if err != nil || captured == nil || captured.Count() != 1 {
		t.Fatal("installed descriptor not projected", captured, err)
	}
	row := captured.Anonymous[0]
	for i, expected := range []ids.OID{ext.Address().ObjectID, ids.OID(ext.Owner.ID()), ext.TargetNamespace.ObjectID} {
		actual, err := row[[]int{0, 2, 3}[i]].OID()
		if err != nil || actual != uint32(expected) {
			t.Fatal("descriptor-derived identity", i, actual, expected, err)
		}
	}
	name, nameErr := row[1].Text()
	version, versionErr := row[5].Text()
	relocatable, relocatableErr := row[4].Boolean()
	if nameErr != nil || versionErr != nil || relocatableErr != nil || name != ext.Name() || version != ext.Version || !relocatable || !row[6].IsNull() || !row[7].IsNull() {
		t.Fatal("installed descriptor attributes", row, nameErr, versionErr, relocatableErr)
	}
	if empty.Count() != 0 || captured.Ref != ref {
		t.Fatal("capture resampled or lost bound identity", empty, captured)
	}
}

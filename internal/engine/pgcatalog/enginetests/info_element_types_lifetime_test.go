package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestInfoElementTypesRetainedAuthority(t *testing.T) {
	f := newElementUnitFixture(t)
	f.f.e.Catalog.PublishLocked(f.state)
	f.f.op.Session.DatabaseName = "captured_database"
	f.f.actor(f.f.member)
	scope, err := f.f.e.StartStatement(f.f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.f.e.Mu.Lock()
	err = f.f.e.CaptureCommentSnapshotLocked(scope)
	f.f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	old := elementUnitInput(t, f.f, f.state, f.f.member)
	retained := elementUnitRows(t, old, "default_schema")
	oldBag := elementUnitBag(t, retained)
	oldCell := retained[0][0]
	oldDomain := old.Source.Columns[0].Usage.Type
	oldElement := old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 48000})
	oldArray := old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 48001})
	oldTable := old.Catalog.MustRelation(f.table.Address())
	oldFormal := f.routine.SQL.Formals[2]
	oldSource := old.Source
	next := catalog.CloneState(f.state)
	// Replace a canonical user element and its array generation together. Every
	// affected declaration migrates; no same-OID foreign pointer is accepted.
	element := *oldElement
	element.SQLType, err = sqltype.Domain(48000, "element_scalar", "default_schema.element_scalar", oldElement.SQLType.DomainBase())
	if err != nil {
		t.Fatal(err)
	}
	element.ObjectRevision = oldElement.Revision() + 1
	array := *oldArray
	array.SQLType, err = sqltype.Array(48001, "_element_scalar", element.SQLType)
	if err != nil {
		t.Fatal(err)
	}
	array.ObjectRevision = oldArray.Revision() + 1
	next.SetDescriptor(element.Address(), &element)
	next.SetDescriptor(array.Address(), &array)
	table := *f.table
	table.ObjectName, table.Owner = "element_table_next", f.f.reader
	table.ObjectRevision = f.table.Revision() + 1
	table.Columns = append([]catalog.Column(nil), table.Columns...)
	table.Columns[2].Usage = array.SQLType.Usage()
	next.SetDescriptor(table.Address(), &table)
	routine := *f.routine
	routine.ObjectName, routine.Owner = "element_routine_next", f.f.reader
	routine.ObjectRevision = f.routine.Revision() + 1
	formals := append([]proc.Formal(nil), routine.SQL.Formals...)
	formals[2].Usage = array.SQLType.Usage()
	definition := *routine.SQL
	if err := definition.SetFormals(formals); err != nil {
		t.Fatal(err)
	}
	routine.SQL = &definition
	next.SetDescriptor(routine.Address(), &routine)
	source := *oldSource
	source.ObjectRevision = oldSource.Revision() + 1
	source.Columns = append([]catalog.Column(nil), source.Columns...)
	next.SetDescriptor(source.Address(), &source)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	var edges []authorization.Membership
	for _, edge := range fixtureEdges(roles) {
		if edge.Role != f.f.owner || edge.Member != f.f.member {
			edges = append(edges, edge)
		}
	}
	setFixtureEdges(roles, edges)
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.e.Catalog.PublishLocked(next)
	f.f.actor(f.f.reader)
	f.f.op.Session.DatabaseName = "new_database"
	// Opening after both publications must use the earlier inquiry's source,
	// database, roles, element pointers and type-name path as one coherent unit.
	view := scope.Inquiry.CommentCatalog
	f.f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.f.e, scope, view.Owner().Domain.Ref(view.Epoch(), oldSource.Address()), oldSource.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.View != nil || captured.Anonymous == nil {
		t.Fatal("not an owned eager capture", captured)
	}
	var selected [][]sqltype.Value
	for _, row := range captured.Anonymous {
		if columnsUnitCell(t, row[1]) == "default_schema" {
			selected = append(selected, row)
		}
	}
	if !reflect.DeepEqual(elementUnitBag(t, selected), oldBag) {
		t.Fatal("captured frontier was resampled")
	}
	current := elementUnitInput(t, f.f, next, f.f.reader)
	if current.Source != &source || current.Source.Revision() <= oldSource.Revision() || current.Catalog.MustType(element.Address()).SQLType == oldElement.SQLType || current.Catalog.MustType(element.Address()).Revision() <= oldElement.Revision() {
		t.Fatal("fixture did not advance effective generations")
	}
	newRows := elementUnitRows(t, current, "default_schema")
	for _, row := range newRows {
		name := columnsUnitCell(t, row[2])
		if name != "element_table_next" && name != "element_routine_next_48101" {
			t.Fatal("current reader inherited stale ownership", name)
		}
	}
	if len(newRows) != 7 {
		t.Fatal("current owner declaration rows", len(newRows))
	}
	elementUnitAssert(t, elementUnitRows(t, elementUnitInput(t, f.f, next, f.f.member), "default_schema"))
	if old.Source != oldSource || old.Catalog.MustType(element.Address()) != oldElement || oldArray.SQLType.Element() != oldElement.SQLType || oldTable.Columns[2].Usage.Type != oldArray.SQLType || f.routine.SQL.Formals[2].Usage != oldFormal.Usage || oldCell.Type() != oldDomain || !oldCell.ValidResult(oldSource.Columns[0].Usage) || !reflect.DeepEqual(elementUnitBag(t, retained), oldBag) {
		t.Fatal("retained definitions or value backing were retagged")
	}
	for _, row := range retained {
		if row[0].Type() != oldDomain || row[6].Type() != oldSource.Columns[6].Usage.Type || !row[6].IsNull() {
			t.Fatal("retained typed NULL generation")
		}
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoElementTypesOID)
	mixed := old
	mixed.Catalog, mixed.Source, mixed.Roles = current.Catalog, current.Source, current.Roles
	if rows, err := produce(mixed); rows != nil || err == nil {
		t.Fatal("mixed paired frontier published", rows, err)
	}
	// A distinct paired role publication rejects the old runtime but works with
	// its own recaptured authority, without mutating the old role reader.
	auth := core.CatalogRuntimeFieldsOf(current.Runtime).Auth
	role, ok := auth.Roles.Find(f.f.reader)
	if !ok {
		t.Fatal("missing current role")
	}
	role.Name = "next_reader"
	root, err := auth.RoleRecord.Root.PutDescriptor(role)
	if err != nil {
		t.Fatal(err)
	}
	paired := auth
	paired.RoleRecord = &access.RoleRecord{Root: root, Revision: auth.RoleRecord.Revision + 1}
	paired.Recapture(current.Catalog)
	mixed = current
	mixed.Roles = paired.Roles
	if rows, err := produce(mixed); rows != nil || err == nil {
		t.Fatal("mixed role frontier published", rows, err)
	}
	mixed.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: paired, Database: "captured_database"})
	if got := elementUnitBag(t, elementUnitRows(t, mixed, "default_schema")); !reflect.DeepEqual(got, elementUnitBag(t, newRows)) {
		t.Fatal("coherent paired frontier rejected", got)
	}
}

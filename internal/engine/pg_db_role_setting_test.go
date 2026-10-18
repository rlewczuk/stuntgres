package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgDBRoleSettingUnitSource(t *testing.T, e *Engine, catalog CatalogRead) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2964}
	d, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_db_role_setting")
	}
	return d, e.catalog.domain.Ref(catalog.Epoch(), address)
}

func pgDBRoleSettingUnitCapture(t *testing.T, e *Engine, catalog CatalogRead) {
	t.Helper()
	d, ref := pgDBRoleSettingUnitSource(t, e, catalog)
	e.mu.Lock()
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	e.mu.Unlock()
	if err != nil || rows == nil || rows.ref != ref || rows.count() != 0 || rows.anonymous == nil || len(rows.anonymous) != 0 {
		t.Fatal("empty owned capture", rows, err)
	}
}

func TestPGDBRoleSettingShapeAndOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d, _ := pgDBRoleSettingUnitSource(t, e, catalog)
	owner := e.Roles().LookupRef("postgres")
	if d.name != "pg_db_role_setting" || d.namespace != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || d.relationKind != catalogRelation || d.virtualView || d.attributeHighWater != 3 || len(d.columns) != 3 || d.owner != owner || !d.hasIndexes {
		t.Fatal("relation identity", d)
	}
	names := [...]string{"setdatabase", "setrole", "setconfig"}
	typeIDs := [...]sqltype.ID{26, 26, 1009}
	collations := [...]uint32{0, 0, 950}
	for i, column := range d.columns {
		typ, ok := catalog.LookupType(typeIDs[i])
		if !ok || column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || uint32(column.Usage.Collation.ID) != collations[i] || column.Nullable != (i == 2) || !column.NullabilityKnown || column.OriginTable != 2964 || column.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[names[i]] != i {
			t.Fatal("column", i, column)
		}
		metadata := relationAttributeMetadata(d, i)
		dimensions := int16(0)
		if i == 2 {
			dimensions = 1
		}
		if metadata.name != names[i] || metadata.typeID != uint32(typeIDs[i]) || metadata.number != int32(i+1) || metadata.typmod != -1 || metadata.notNull != (i < 2) || metadata.dimensions != dimensions || metadata.collation != collations[i] {
			t.Fatal("catalog attribute projection", i, metadata)
		}
	}
	if _, exists := d.index["oid"]; exists {
		t.Fatal("invented oid column")
	}
	if d.columns[2].Usage.Type.Element().ID() != 25 {
		t.Fatal("text array element")
	}
	if err := (relationProvider{}).Validate(catalog, d); err != nil {
		t.Fatal(err)
	}
	if !d.acl.Explicit || len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Grantor != owner || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	row := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10054})
	array := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10053})
	ownedRow, ok := catalog.LookupType(10054)
	ownedArray, arrayOK := catalog.LookupArray(ownedRow)
	if d.rowType != 10054 || !ok || !arrayOK || row.typ != ownedRow || array.typ != ownedArray || row.array != 10053 || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != owner || array.owner != owner {
		t.Fatal("row/array ownership", row, array)
	}
	if !representedCatalogHasIndexes(2964) || !representedSummaryTable(2964) || pgClassKind(catalog, d) != "r" || pgClassPersistence(catalog, d) != "p" {
		t.Fatal("table discovery")
	}
	class := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	facts, err := e.pgClassRow(context.Background(), catalog, e.Roles(), class, d)
	if err != nil {
		t.Fatal(err)
	}
	if shared, err := facts[15].Boolean(); err != nil || !shared {
		t.Fatal("shared", shared, err)
	}
	if space, err := facts[8].OID(); err != nil || space != 1664 {
		t.Fatal("pg_global", space, err)
	}
	for _, oid := range []ids.OID{2965, 2966, 2967} {
		if _, exists := catalog.Find(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}); exists {
			t.Fatal("fabricated backing relation", oid)
		}
	}
}

func TestPGDBRoleSettingCapture(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d, ref := pgDBRoleSettingUnitSource(t, e, catalog)
	state, oid, revision := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision
	descriptors, names := maps.Clone(state.descriptors), maps.Clone(state.names)
	unchanged := func() {
		if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || !maps.Equal(descriptors, state.descriptors) || !maps.Equal(names, state.names) {
			t.Fatal("capture mutated catalog/frontier")
		}
	}
	for range 3 {
		pgDBRoleSettingUnitCapture(t, e, catalog)
		unchanged()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.mu.Lock()
	rows, err := e.captureCatalogSourceLocked(ctx, catalog, e.roleRead(catalog, e.roles), ref, d)
	e.mu.Unlock()
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", rows, err)
	}
	unchanged()
}

func TestPGDBRoleSettingSessionDefaultsExcluded(t *testing.T) {
	def, err := ValidateSetting(SettingDefinition{Name: "dbrole18.mode", Type: SettingString, BootDefault: "registered"})
	if err != nil {
		t.Fatal(err)
	}
	e := NewWithSettings([]SettingDefinition{def}, "", false)
	provider := &settingsDefaultProviderForTest{values: map[string]string{"application_name": "captured_first", "dbrole18.mode": "first"}, calls: map[string]int{}}
	a := sessionStart(t, e, provider)
	provider.values["application_name"], provider.values["dbrole18.mode"] = "captured_second", "second"
	b := sessionStart(t, e, provider)
	if a.settings.defaults["application_name"] == b.settings.defaults["application_name"] {
		t.Fatal("different captured defaults not established")
	}
	if a.settings.defaults["dbrole18.mode"] == b.settings.defaults["dbrole18.mode"] {
		t.Fatal("different registered defaults not established")
	}
	pgDBRoleSettingUnitCapture(t, e, e.CatalogRead())
	for _, s := range []*Session{a, b} {
		for _, q := range []string{"SET application_name='override'", "SET dbrole18.mode='override'", "SET dbrole18.placeholder='custom'", "BEGIN", "SET LOCAL application_name='local'", "ROLLBACK", "RESET ALL"} {
			sessionSQL(t, s, q)
			pgDBRoleSettingUnitCapture(t, e, e.CatalogRead())
		}
	}
}

func TestPGDBRoleSettingAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER dbrole18_reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION dbrole18_reader")
	ast := statement(t, "SELECT * FROM pg_db_role_setting")
	held, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	execute := func(ast parser.Statement, held StatementDescription, denied bool) {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &held
		result, executeErr := scope.Execute(ast, 0, nil)
		err = scope.Finish(executeErr)
		if denied {
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_db_role_setting"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("empty-source denial", err)
			}
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("denial published rows")
			}
			return
		}
		if err != nil || result.Rows == nil {
			t.Fatal("ordinary reader", err)
		}
		if !reflect.DeepEqual(result.Rows.Schema(), held.Results) {
			t.Fatal("empty reader metadata", result.Rows.Schema(), held.Results)
		}
		defer result.Rows.Close()
		if next, err := result.Rows.Next(context.Background(), make([]sqltype.Value, len(held.Results))); next || err != nil {
			t.Fatal("empty reader result", next, err)
		}
	}
	publish := func(table, column bool) {
		base := e.CatalogRead()
		d, _ := pgDBRoleSettingUnitSource(t, e, base)
		next := *d
		next.revision++
		next.acl = authorization.ACLValue{Explicit: true}
		next.columnACL = nil
		if table {
			next.acl.Entries = []authorization.ACLEntry{{Grantor: d.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}
		}
		if column {
			next.columnACL = map[int32]authorization.ACLValue{
				1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}},
			}
		}
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		publication, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
	}
	execute(ast, held, false)
	publish(false, false)
	execute(ast, held, true)
	roleError(t, e, op, "SELECT setrole FROM pg_db_role_setting", "42501", "permission denied for table pg_db_role_setting", "")
	publish(true, false)
	execute(ast, held, false)

	columnAST := statement(t, "SELECT setdatabase FROM pg_db_role_setting")
	columnHeld, err := e.Describe(op, columnAST)
	if err != nil {
		t.Fatal(err)
	}
	if len(columnHeld.Results) != 1 || columnHeld.Results[0].Usage.Type.ID() != sqltype.Oid || columnHeld.Results[0].Nullable || !columnHeld.Results[0].NullabilityKnown || columnHeld.Results[0].OriginTable != 2964 || columnHeld.Results[0].OriginAttribute != 1 {
		t.Fatal("column-only metadata", columnHeld.Results)
	}
	publish(false, true)
	execute(columnAST, columnHeld, false)
	execute(ast, held, true)
	roleError(t, e, op, "SELECT setrole FROM pg_db_role_setting", "42501", "permission denied for table pg_db_role_setting", "")
	publish(false, false)
	execute(columnAST, columnHeld, true)
	publish(false, true)
	execute(columnAST, columnHeld, false)
	execute(ast, held, true)
	publish(true, false)
	execute(ast, held, false)
}

package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgDBRoleSettingUnitSource(t *testing.T, e *core.Engine, catalog catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2964}
	d, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_db_role_setting")
	}
	return d, e.Catalog.Domain.Ref(catalog.Epoch(), address)
}

func pgDBRoleSettingUnitCapture(t *testing.T, e *core.Engine, catalog catalog.Read) {
	t.Helper()
	d, ref := pgDBRoleSettingUnitSource(t, e, catalog)
	e.Mu.Lock()
	rows, err := captureCatalogSourceLocked(e, context.Background(), catalog, e.RoleStore.PublishedRead(&e.Catalog.Owner, catalog), ref, d)
	e.Mu.Unlock()
	if err != nil || rows == nil || rows.Ref != ref || rows.Count() != 0 || rows.Anonymous == nil || len(rows.Anonymous) != 0 {
		t.Fatal("empty owned capture", rows, err)
	}
}

func TestPGDBRoleSettingShapeAndOwnership(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d, _ := pgDBRoleSettingUnitSource(t, e, view)
	owner := e.Roles().LookupRef("postgres")
	if d.Name() != "pg_db_role_setting" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || d.RelationKind != catalog.CatalogRelation || d.VirtualView || d.ColumnMeta.HighWater != 3 || len(d.Columns) != 3 || d.Owner != owner || !d.History.HasIndexes {
		t.Fatal("relation identity", d)
	}
	names := [...]string{"setdatabase", "setrole", "setconfig"}
	typeIDs := [...]sqltype.ID{26, 26, 1009}
	collations := [...]uint32{0, 0, 950}
	for i, column := range d.Columns {
		typ, ok := view.LookupType(typeIDs[i])
		if !ok || column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || uint32(column.Usage.Collation.ID) != collations[i] || column.Nullable != (i == 2) || !column.NullabilityKnown || column.OriginTable != 2964 || column.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[names[i]] != i {
			t.Fatal("column", i, column)
		}
		metadata := pgcatalog.AttributeMetadata(d, i)
		dimensions := int16(0)
		if i == 2 {
			dimensions = 1
		}
		if metadata.Name != names[i] || metadata.TypeID != uint32(typeIDs[i]) || metadata.Number != int32(i+1) || metadata.Typmod != -1 || metadata.NotNull != (i < 2) || metadata.Dimensions != dimensions || metadata.Collation != collations[i] {
			t.Fatal("catalog attribute projection", i, metadata)
		}
	}
	if _, exists := d.Index["oid"]; exists {
		t.Fatal("invented oid column")
	}
	if d.Columns[2].Usage.Type.Element().ID() != 25 {
		t.Fatal("text array element")
	}
	if err := (core.RelationProvider).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Grantor != owner || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10054})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10053})
	ownedRow, ok := view.LookupType(10054)
	ownedArray, arrayOK := view.LookupArray(ownedRow)
	if d.RowType != 10054 || !ok || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 10053 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != owner || array.Owner != owner {
		t.Fatal("row/array ownership", row, array)
	}
	if !pgcatalog.CatalogHasIndexes(2964) || !pgcatalog.SummaryTable(2964) || pgcatalog.ClassKind(view, d) != "r" || pgcatalog.ClassPersistence(view, d) != "p" {
		t.Fatal("table discovery")
	}
	class := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	facts, err := pgcatalog.ClassRow(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Owner: &e.Catalog.Owner, Source: class, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}, d)
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
		if _, exists := view.Find(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}); exists {
			t.Fatal("fabricated backing relation", oid)
		}
	}
}

func TestPGDBRoleSettingCapture(t *testing.T) {
	e := engine.New()
	catalog := e.CatalogRead()
	d, ref := pgDBRoleSettingUnitSource(t, e, catalog)
	state, oid, revision := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision()
	descriptors, names := maps.Collect(state.Descriptors()), maps.Collect(state.Names())
	unchanged := func() {
		if e.Catalog.State != state || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || !maps.Equal(descriptors, maps.Collect(state.Descriptors())) || !maps.Equal(names, maps.Collect(state.Names())) {
			t.Fatal("capture mutated catalog/frontier")
		}
	}
	for range 3 {
		pgDBRoleSettingUnitCapture(t, e, catalog)
		unchanged()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Mu.Lock()
	rows, err := captureCatalogSourceLocked(e, ctx, catalog, e.RoleStore.PublishedRead(&e.Catalog.Owner, catalog), ref, d)
	e.Mu.Unlock()
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", rows, err)
	}
	unchanged()
}

func TestRoleSettingsPopulatedCatalogCapture(t *testing.T) {
	e := engine.New()
	login := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	key := access.RoleSettingKey{Role: login}
	first := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "first"}})
	// A second real scope distinguishes changed-row copying from root rebuilding.
	databaseKey := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
	record := roleDefaultsInstall(e, databaseKey, []access.RoleSetting{{Name: "work_mem", Value: "1536kB"}})
	roleRow, databaseRow := record.Settings.Lookup(key), record.Settings.Lookup(databaseKey)
	if record == first || !record.SameAuthority(first) || record.Settings == first.Settings || &roleRow[0] != &first.Settings.Lookup(key)[0] {
		t.Fatal("complete publication did not retain unchanged graph and row backing")
	}
	s := roleDefaultsStart(t, e, session.SessionStart{})
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	view := e.CatalogRead()
	d, ref := pgDBRoleSettingUnitSource(t, e, view)
	captureScope := func() *core.StatementScope {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth := catalogAuthorization(e, view)
		if auth.RoleRecord != e.RoleStore.Published || !auth.RoleRecord.SameAuthority(record) || auth.Roles.LookupRef("postgres") != login {
			t.Fatal("capture lacks actual paired role authority")
		}
		return &core.StatementScope{Op: op, Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: view, CommentRoles: auth.Roles, DeparseAuth: auth})}
	}
	capture := func(scope *core.StatementScope) *exec.Source {
		e.Mu.Lock()
		rows, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, d)
		// The real capture owner consumes Engine.Mu.
		if err != nil || rows == nil || rows.Ref != ref || rows.Anonymous == nil {
			t.Fatal("populated capture", rows, err)
		}
		return rows
	}
	check := func(rows *exec.Source, roleValue string) {
		t.Helper()
		wantCount := 1
		if roleValue != "" {
			wantCount++
		}
		if rows.Count() != wantCount {
			t.Fatal("scope count", rows.Count(), wantCount)
		}
		seenRole, seenDatabase := false, false
		for _, row := range rows.Anonymous {
			database, databaseErr := row[0].OID()
			role, roleErr := row[1].OID()
			value := "work_mem=1536kB"
			switch {
			case database == 0 && role == uint32(authorization.BootstrapPrincipalID) && roleValue != "":
				seenRole = true
				value = "application_name=" + roleValue
			case database == uint32(databaseKey.Database.ObjectID) && role == 0:
				seenDatabase = true
			default:
				t.Fatal("unexpected persisted scope", database, role)
			}
			want, err := sqltype.OwnedArrayValue(d.Columns[2].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{sqltype.String(d.Columns[2].Usage.Type.Element(), value)})
			if databaseErr != nil || roleErr != nil || err != nil || !reflect.DeepEqual(row[2], want) {
				t.Fatal("persisted materialized row", row, databaseErr, roleErr, err)
			}
		}
		if !seenDatabase || seenRole != (roleValue != "") {
			t.Fatal("missing persisted scope")
		}
	}
	oldScope := captureScope()
	if core.InquiryDeparseAuth(&oldScope.Inquiry).RoleRecord != record {
		t.Fatal("inquiry did not retain complete record")
	}
	old := capture(oldScope)
	oldBacking := &old.Anonymous[0][0]
	check(old, "first")
	replaced := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "application_name", Value: "second"}})
	if replaced == record || replaced.Settings == record.Settings || !replaced.SameAuthority(record) || &replaced.Settings.Lookup(key)[0] == &roleRow[0] || &replaced.Settings.Lookup(databaseKey)[0] != &databaseRow[0] {
		t.Fatal("replacement violated immutable root/row ownership")
	}
	check(capture(captureScope()), "second")
	check(capture(oldScope), "first")
	removed := roleDefaultsInstall(e, key, nil)
	if removed == replaced || removed.Settings == replaced.Settings || !removed.SameAuthority(record) || len(removed.Settings.Lookup(key)) != 0 || &removed.Settings.Lookup(databaseKey)[0] != &databaseRow[0] {
		t.Fatal("removal violated immutable root/row ownership")
	}
	check(capture(captureScope()), "")
	check(capture(oldScope), "first")
	check(old, "first")
	if &old.Anonymous[0][0] != oldBacking || &record.Settings.Lookup(key)[0] != &roleRow[0] || roleRow[0].Value != "first" {
		t.Fatal("retained materialized output or captured input backing changed")
	}
}

func TestPGDBRoleSettingSessionDefaultsExcluded(t *testing.T) {
	def, err := settings.Validate(settings.Definition{Name: "dbrole18.mode", Type: settings.String, BootDefault: "registered"})
	if err != nil {
		t.Fatal(err)
	}
	e := engine.NewWithSettings([]settings.Definition{def}, "", false)
	provider := &settingsDefaultProviderForTest{values: map[string]string{"application_name": "captured_first", "dbrole18.mode": "first"}, calls: map[string]int{}}
	a := enginetest.StartSession(t, e, provider)
	provider.values["application_name"], provider.values["dbrole18.mode"] = "captured_second", "second"
	b := enginetest.StartSession(t, e, provider)
	if a.Settings.Defaults["application_name"] == b.Settings.Defaults["application_name"] {
		t.Fatal("different captured defaults not established")
	}
	if a.Settings.Defaults["dbrole18.mode"] == b.Settings.Defaults["dbrole18.mode"] {
		t.Fatal("different registered defaults not established")
	}
	pgDBRoleSettingUnitCapture(t, e, e.CatalogRead())
	for _, s := range []*session.Session{a, b} {
		for _, q := range []string{"SET application_name='override'", "SET dbrole18.mode='override'", "SET dbrole18.placeholder='custom'", "BEGIN", "SET LOCAL application_name='local'", "ROLLBACK", "RESET ALL"} {
			enginetest.SessionSQL(t, s, q)
			pgDBRoleSettingUnitCapture(t, e, e.CatalogRead())
		}
	}
}

func TestPGDBRoleSettingAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER dbrole18_reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION dbrole18_reader")
	ast := enginetest.Statement(t, "SELECT * FROM pg_db_role_setting")
	held, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	execute := func(ast parser.Statement, held core.Description, denied bool) {
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
		next.ObjectRevision++
		next.Security.ACL = authorization.ACLValue{Explicit: true}
		next.Security.ColumnACL = nil
		if table {
			next.Security.ACL.Entries = []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}
		}
		if column {
			next.Security.ColumnACL = map[int32]authorization.ACLValue{
				1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}},
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
		if _, err := enginetest.PublishCatalog(e, publication, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
	}
	execute(ast, held, false)
	publish(false, false)
	execute(ast, held, true)
	enginetest.RoleError(t, e, op, "SELECT setrole FROM pg_db_role_setting", "42501", "permission denied for table pg_db_role_setting", "")
	publish(true, false)
	execute(ast, held, false)

	columnAST := enginetest.Statement(t, "SELECT setdatabase FROM pg_db_role_setting")
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
	enginetest.RoleError(t, e, op, "SELECT setrole FROM pg_db_role_setting", "42501", "permission denied for table pg_db_role_setting", "")
	publish(false, false)
	execute(columnAST, columnHeld, true)
	publish(false, true)
	execute(columnAST, columnHeld, false)
	execute(ast, held, true)
	publish(true, false)
	execute(ast, held, false)
}

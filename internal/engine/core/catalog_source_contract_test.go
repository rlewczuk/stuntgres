package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCatalogSourceProducerCoverage(t *testing.T) {
	e := engine.New()
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "producer_contract_handler"}, Wrapper: &fdwUnitWrapper{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, statement := range []string{
		"CREATE TABLE producer_contract_row(id INT DEFAULT 7)",
		"CREATE INDEX producer_contract_index ON producer_contract_row(id)",
		"CREATE VIEW producer_contract_view AS SELECT id FROM producer_contract_row",
		"CREATE SEQUENCE producer_contract_sequence",
		"CREATE FOREIGN DATA WRAPPER producer_contract_fdw HANDLER producer_contract_handler",
		"CREATE SERVER producer_contract_server FOREIGN DATA WRAPPER producer_contract_fdw",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER producer_contract_server OPTIONS (token 'private')",
		"CREATE FOREIGN TABLE producer_contract_foreign(id INT OPTIONS (secret 'column=actual')) SERVER producer_contract_server",
	} {
		enginetest.RoleExecute(t, e, op, statement)
	}
	if err := validateCatalogSourceProducers(pgcatalog.Registrations()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]pgcatalog.Registration{
		{{1247, capturePGType}, {1247, capturePGAttribute}},
		{{pgcatalog.LocksOID, capturePGType}},
		{{12048, capturePGType}},
		{{1247, nil}},
	} {
		if validateCatalogSourceProducers(invalid) == nil {
			t.Fatal("ambiguous or missing dispatch ownership accepted")
		}
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	// Direct opening bypasses query collection, which normally captures this
	// statement-wide catalog/role/presentation frontier before any source opens.
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	seen := make(map[ids.OID]bool)
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || d.RelationKind != catalog.CatalogRelation {
			continue
		}
		oid := d.Address().ObjectID
		_, producer := pgcatalog.ProducerFor(oid)
		if producer == explicitCatalogSource(oid) {
			t.Fatal("missing or overlapping source owner", d.Name())
		}
		ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
		e.Mu.Lock()
		source, err := core.EngineOpenRelationSourceLocked(e, scope, ref, d.Owner, plan.RowMark{}, nil, nil)
		if err != nil || source == nil {
			t.Fatal(d.Name(), err)
		}
		if source.Ref != ref {
			t.Fatal("wrapper lost ref", d.Name())
		}
		if oid == 12048 {
			if source.PGSequences == nil || len(source.Anonymous) != 1 || !source.Anonymous[0][10].IsNull() {
				t.Fatal("sequence source lost lazy ownership")
			}
			source.Close()
			seen[oid] = true
			continue
		}
		for _, row := range source.Anonymous {
			if len(row) != len(d.Columns) {
				t.Fatal("source row shape", d.Name(), len(row))
			}
			for i, value := range row {
				if value.Type() != d.Columns[i].Usage.Type || !value.ValidResult(d.Columns[i].Usage) {
					t.Fatal("source carrier", d.Name(), i, value)
				}
			}
		}
		switch oid {
		case 1259, 1249, 2610, 2604, 2618, 12000, 12002, 12003, 2224,
			ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID, ids.ForeignTableClassID:
			if len(source.Anonymous) == 0 {
				t.Fatal("real fixture missing from producer", d.Name())
			}
		case pgcatalog.InfoColumnsOID:
			var matches int
			for _, row := range source.Anonymous {
				table := infoUserMappingsUnitText(t, row[2])
				column := infoUserMappingsUnitText(t, row[3])
				if table == "producer_contract_row" && column == "id" {
					base, _ := row[4].BaseValue()
					ordinal, _ := base.Integer()
					defaultValue, err := row[5].BaseValue()
					if err != nil {
						t.Fatal("columns default carrier", err)
					}
					expression, err := defaultValue.Text()
					if err != nil {
						t.Fatal("columns default text", err)
					}
					if ordinal != 1 || expression != "7" {
						t.Fatal("columns fixture metadata", row)
					}
					matches++
				}
			}
			if matches != 1 {
				t.Fatal("columns fixture missing or duplicated", matches)
			}
		case pgcatalog.InfoColumnOptionsOID:
			if len(source.Anonymous) != 1 {
				t.Fatal("actual foreign column option missing", source.Anonymous)
			}
			for i, want := range []string{op.Session.DatabaseName, "public", "producer_contract_foreign", "id", "secret", "column=actual"} {
				base, err := source.Anonymous[0][i].BaseValue()
				if err != nil {
					t.Fatal(err)
				}
				got, err := base.Text()
				if err != nil || got != want {
					t.Fatal("column option coherent capture", i, got, want, err)
				}
			}
		case pgcatalog.InfoUserMappingsOID:
			if len(source.Anonymous) != 1 {
				t.Fatal("actual information-schema mapping missing")
			}
			for i, want := range []string{"postgres", op.Session.DatabaseName, "producer_contract_server"} {
				got := infoUserMappingsUnitText(t, source.Anonymous[0][i])
				if got != want {
					t.Fatal("mapping coherent capture", i, got, want)
				}
			}
		case pgcatalog.UserMappingsOID:
			if len(source.Anonymous) != 1 || source.Anonymous[0][5].IsNull() || source.Anonymous[0][5].ArrayLen() != 1 {
				t.Fatal("fresh mapping disclosure lost")
			}
			option, err := source.Anonymous[0][5].ArrayElement(0).Text()
			if err != nil || option != "token=private" {
				t.Fatal("foreign mapping option", option, err)
			}
		case 1247:
			found := false
			for _, row := range source.Anonymous {
				if id, _ := row[0].OID(); id == 23 {
					name, _ := row[1].Text()
					found = name == "int4"
				}
			}
			if !found {
				t.Fatal("pg_type lost builtin identity")
			}
		case pgcatalog.SettingsOID:
			row := settingsMetadataRow(t, source.Anonymous, "row_security")
			name, _ := row[0].Text()
			value, _ := row[1].Text()
			if name != "row_security" || value != "on" {
				t.Fatal("physical setting route", name, value)
			}
		}
		source.Close()
		seen[oid] = true
	}
	for _, entry := range pgcatalog.Registrations() {
		if !seen[entry.OID] {
			t.Fatal("producer has no registered consumer", entry.OID)
		}
	}
	for _, virtual := range []bool{false, true} {
		unknown := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TypeOID})
		unknown.ObjectAddress.ObjectID = 299999
		unknown.VirtualView = virtual
		e.Mu.Lock()
		e.Catalog.State.SetDescriptor(unknown.Address(), &unknown)
		ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), unknown.Address())
		source, err := core.EngineOpenRelationSourceLocked(e, scope, ref, unknown.Owner, plan.RowMark{}, nil, nil)
		want := "stuntgres: unknown catalog source"
		if virtual {
			want = "stuntgres: unknown virtual catalog view"
		}
		if source != nil || err == nil || err.Error() != want {
			t.Fatal("unknown source diagnostic", source, err)
		}
	}
}

func TestCatalogSourceEmptyMaterializedRows(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	d := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12006})
	produce, ok := pgcatalog.ProducerFor(d.Address().ObjectID)
	if !ok {
		t.Fatal("missing materialized catalog producer")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: e.Roles(), Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), d.Address()), Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.Anonymous == nil || len(source.Anonymous) != 0 || source.View != nil {
		t.Fatal("empty catalog rows became a missing physical source", source)
	}
}

func TestCatalogSourceInputProvenance(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TypeOID})
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.Roles(), Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{}}
	rows, err := capturePGType(in)
	if err != nil || len(rows) == 0 {
		t.Fatal(err)
	}
	bad := in
	bad.Owner = &foreign.Catalog.Owner
	if got, err := capturePGType(bad); got != nil || err == nil || err.Error() != "stuntgres: foreign type source authority" {
		t.Fatal("numeric identities substituted for owner", err)
	}
	bad = in
	bad.Roles = foreign.Roles()
	if got, err := capturePGType(bad); got != nil || err == nil || err.Error() != "stuntgres: foreign type owner authority" {
		t.Fatal("foreign principal publication admitted", err)
	}
	// New input fields must not permit a producer to combine different paired
	// role publications with its runtime authority, even inside one owner.
	op, done := enginetest.RoleOperation(t, e, "postgres")
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.DataTypePrivilegesOID})
	mixed := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: foreign.Roles(), Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(view.Epoch(), d.Address()), Source: d, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
	if got, err := captureDataTypePrivileges(mixed); got != nil || err == nil {
		done()
		t.Fatal("mixed input/runtime roles admitted", err)
	}
	d = view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserMappingsOID})
	mixed.Ref, mixed.Source = e.Catalog.Domain.Ref(view.Epoch(), d.Address()), d
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoUserMappingsOID)
	if !ok {
		done()
		t.Fatal("missing user_mappings producer")
	}
	if got, err := produce(mixed); got != nil || err == nil {
		done()
		t.Fatal("mixed user_mappings roles admitted", err)
	}
	done()
	retained := append([]sqltype.Value(nil), rows[0]...)
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	again, err := capturePGType(in)
	if err != nil || len(again) != len(rows) || !reflect.DeepEqual(retained, rows[0]) {
		t.Fatal("coherent retained roots rejected/mutated", err)
	}
	bad = in
	bad.Catalog = e.CatalogRead()
	bad.Ref = e.Catalog.Domain.Ref(bad.Catalog.Epoch(), source.Address())
	if got, err := capturePGType(bad); got != nil || err == nil {
		t.Fatal("old source paired with new generation", err)
	}
	wrapped, err := core.CaptureCatalogSourceRows(in, capturePGType)
	if err != nil || wrapped.Ref != in.Ref || len(wrapped.Anonymous) != len(rows) {
		t.Fatal("actual row wrapper ownership", err)
	}
}

func TestCatalogSourceRuntimeCapture(t *testing.T) {
	t.Run("view_table_usage_database_frontier", infoViewTableUsageCapture)
	t.Run("user_mappings_database_frontier", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		state := catalog.CloneState(f.e.Catalog.State)
		server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 20300}, ObjectName: "capture_server", ObjectRevision: 1, Owner: f.owner}
		mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 20301}, ObjectName: catalog.ForeignMappingName(server.Address(), f.reader), ObjectRevision: 1, Server: server.Address(), MappedRole: f.reader}
		state.SetDescriptor(server.Address(), server)
		state.SetDescriptor(mapping.Address(), mapping)
		f.e.Catalog.PublishLocked(state)
		f.op.Session.DatabaseName = "physical_capture_database"
		scope, err := f.e.StartStatement(f.op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}()
		f.e.Mu.Lock()
		err = f.e.CaptureCommentSnapshotLocked(scope)
		f.e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		view := scope.Inquiry.CommentCatalog
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserMappingsOID})
		f.e.Mu.Lock()
		source, err := core.EngineOpenRelationSourceLocked(f.e, scope, view.Owner().Domain.Ref(view.Epoch(), d.Address()), d.Owner, plan.RowMark{}, nil, nil)
		if err != nil || source == nil {
			t.Fatal("coherent mapping source", err)
		}
		defer source.Close()
		if len(source.Anonymous) != 1 {
			t.Fatal("actual mapping capture", source.Anonymous)
		}
		for i, want := range []string{"default_reader", "physical_capture_database", "capture_server"} {
			got := infoUserMappingsUnitText(t, source.Anonymous[0][i])
			if got != want {
				t.Fatal("paired physical capture", i, got, want)
			}
		}
	})
	t.Run("cursor_and_settings_dispatch", func(t *testing.T) {
		s := cursorUnitSetup(t)
		e := core.EngineOf(s)
		enginetest.SessionSQL(t, s, "DECLARE producer_runtime CURSOR FOR SELECT i FROM unit_rows")
		enginetest.SessionSQL(t, s, "SET row_security = on")
		cursor := core.PayloadOf(s).Cursors.Entries["producer_runtime"]
		op := enginetest.TransactionOperation(t, s)
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}()
		for _, oid := range []ids.OID{pgcatalog.CursorsOID, pgcatalog.SettingsOID} {
			d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
			e.Mu.Lock()
			source, err := core.EngineOpenRelationSourceLocked(e, scope, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address()), d.Owner, plan.RowMark{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if oid == pgcatalog.CursorsOID {
				name, _ := source.Anonymous[0][0].Text()
				if name != "producer_runtime" || cursor.Place != exec.CursorBefore || core.CursorStage(cursor) != nil {
					t.Fatal("metadata dispatch demanded or replaced cursor")
				}
			} else {
				row := settingsMetadataRow(t, source.Anonymous, "row_security")
				value, _ := row[1].Text()
				provenance, _ := row[8].Text()
				reset, _ := row[13].Text()
				if value != "on" || reset != "on" || provenance != "session" {
					t.Fatal("override presence/coherent tuple", value, reset, provenance)
				}
			}
			source.Close()
		}
	})
	t.Run("prepared_published_owner", func(t *testing.T) {
		e := engine.New()
		admin, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, admin, "CREATE ROLE producer_runtime_owner LOGIN")
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION producer_runtime_owner")
		enginetest.SessionSQL(t, s, "BEGIN")
		if err := preparedUnitControl(t, e, s, parser.KindPrepareTransaction, "producer_runtime_gid"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := preparedUnitControl(t, e, s, parser.KindRollbackPrepared, "producer_runtime_gid"); err != nil {
				t.Error(err)
			}
		}()
		retained := e.Roles()
		// Role-rename SQL is not shipped. Publish the immutable descriptor through
		// the existing authority/session path without changing the owner identity.
		if err := func() error {
			e.Mu.Lock()
			defer e.Mu.Unlock()
			auth, err := e.AuthorizationLocked(admin)
			if err != nil {
				return err
			}
			actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
			if err != nil {
				return err
			}
			if !actor.Superuser {
				return fmt.Errorf("prepared owner fixture requires superuser authority")
			}
			owner, ok := auth.Roles.Lookup("producer_runtime_owner")
			if !ok {
				return fmt.Errorf("prepared owner fixture role missing")
			}
			next := cloneRoleFixture(auth.RoleRecord)
			owner.Name = "producer_runtime_renamed"
			putFixtureRole(next, owner)
			admin.Session.Mu.Lock()
			defer admin.Session.Mu.Unlock()
			if err := core.PublicationOperationLocked(e, admin); err != nil {
				return err
			}
			core.InstallOperationRolesLocked(e, admin, next)
			return nil
		}(); err != nil {
			t.Fatal(err)
		}
		if owner, ok := retained.Lookup("producer_runtime_owner"); !ok || owner.Name != "producer_runtime_owner" {
			t.Fatal("published rename mutated retained input roles")
		}
		view := e.CatalogRead()
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12001})
		in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: retained, Owner: &e.Catalog.Owner, Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}
		e.Mu.Lock()
		expectedXID := e.Visibility.AssignedXIDLocked(core.EnginePreparedRef(e).Lookup("producer_runtime_gid").Ref)
		rows, err := capturePreparedSource(in)
		e.Mu.Unlock()
		if err != nil || len(rows) != 1 {
			t.Fatal("actual prepared registry", err)
		}
		gid, _ := rows[0][1].Text()
		name, _ := rows[0][3].Text()
		xid, err := rows[0][0].XID()
		if err != nil || expectedXID == 0 || xid != expectedXID || gid != "producer_runtime_gid" || name != "producer_runtime_renamed" {
			t.Fatal("assigned XID or published owner lost", xid, gid, name, err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		in.Context = canceled
		e.Mu.Lock()
		got, err := capturePreparedSource(in)
		e.Mu.Unlock()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("prepared cancellation published prefix", err)
		}
	})
}

func TestCatalogSourceSpecMetadata(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	stableAttributes := map[ids.OID][]int32{
		1255: {1, 2, 3, 4, 17, 19, 15, 13, 16, 12, 23, 5, 14, 7, 26, 20, 8, 21, 22, 30},
		2610: {1, 2, 5, 7, 11, 13, 14, 6, 16, 17, 18, 19},
	}
	for _, descriptor := range view.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.CatalogRelation {
			continue
		}
		if stable := stableAttributes[relation.Address().ObjectID]; stable != nil && len(stable) != len(relation.Columns) {
			t.Fatal("stable attribute inventory arity", relation.Name(), len(relation.Columns), len(stable))
		}
		for i, column := range relation.Columns {
			attribute := int32(i + 1)
			if stable := stableAttributes[relation.Address().ObjectID]; stable != nil {
				attribute = stable[i]
			}
			if column.OriginTable != uint32(relation.Address().ObjectID) || column.OriginAttribute != int16(attribute) || !column.NullabilityKnown || relation.Attributes[i] != attribute {
				t.Fatal("column provenance", relation.Name(), i)
			}
		}
		if relation.RowType == 0 {
			continue
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
		if row.OwningRelation != relation.Address() || row.Owner != relation.Owner {
			t.Fatal("row relation association", relation.Name())
		}
		if row.Array != 0 {
			array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
			if array.OwningRelation != relation.Address() || array.Owner != relation.Owner || array.SQLType.Element() != row.SQLType {
				t.Fatal("actual array relation association", relation.Name())
			}
		}
	}
	for _, fixture := range []struct {
		oid        ids.OID
		names      []string
		types      []sqltype.ID
		nullable   []bool
		collations []uint32
		typmods    []int32
	}{
		{1255,
			[]string{"oid", "proname", "pronamespace", "proowner", "pronargs", "prorettype", "provolatile", "proisstrict", "proparallel", "prokind", "proargnames", "prolang", "proretset", "prorows", "prosrc", "proargtypes", "provariadic", "proallargtypes", "proargmodes", "proacl"},
			[]sqltype.ID{26, 19, 26, 26, 21, 26, 18, 16, 18, 18, 1009, 26, 16, 700, 25, 30, 26, 1028, 1002, 1034},
			[]bool{false, false, false, false, false, false, false, false, false, false, true, false, false, false, false, false, false, true, true, true},
			[]uint32{0, 950, 0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0, 0, 100, 0, 0, 0, 0, 0},
			[]int32{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}},
		{2610,
			[]string{"indexrelid", "indrelid", "indisunique", "indisprimary", "indisvalid", "indisready", "indislive", "indnullsnotdistinct", "indkey", "indcollation", "indclass", "indoption"},
			[]sqltype.ID{26, 26, 16, 16, 16, 16, 16, 16, 22, 30, 30, 22},
			[]bool{false, false, false, false, false, false, false, false, false, false, false, false},
			[]uint32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			[]int32{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}},
		{1418, []string{"oid", "umuser", "umserver", "umoptions"}, []sqltype.ID{26, 26, 26, 1009}, []bool{false, false, false, true}, []uint32{0, 0, 0, 950}, []int32{-1, -1, -1, -1}},
		{pgcatalog.RulesOID, []string{"schemaname", "tablename", "rulename", "definition"}, []sqltype.ID{19, 19, 19, 25}, []bool{true, true, true, true}, []uint32{950, 950, 950, 100}, []int32{-1, -1, -1, -1}},
		{16200, []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"}, []sqltype.ID{19, 19, 19, 19, 19, 1043, 1043, 1043}, []bool{true, true, true, true, true, true, true, true}, []uint32{950, 950, 950, 950, 950, 950, 950, 950}, []int32{-1, -1, -1, -1, -1, -1, 7, 7}},
		{16201, []string{"object_catalog", "object_schema", "object_name", "object_type", "dtd_identifier"}, []sqltype.ID{19, 19, 19, 1043, 19}, []bool{true, true, true, true, true}, []uint32{950, 950, 950, 950, 950}, []int32{-1, -1, -1, -1, -1}},
		{16203, []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name"}, []sqltype.ID{13248, 13248, 13248}, []bool{true, true, true}, []uint32{950, 950, 950}, []int32{-1, -1, -1}},
	} {
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: fixture.oid})
		if len(d.Columns) != len(fixture.names) {
			t.Fatal("spec arity", d.Name())
		}
		for i, c := range d.Columns {
			if c.Name != fixture.names[i] || c.Usage.Type.ID() != fixture.types[i] || c.Nullable != fixture.nullable[i] || c.Usage.Collation.ID != fixture.collations[i] || c.Usage.Typmod != fixture.typmods[i] {
				t.Fatal("independent spec metadata", d.Name(), i, c)
			}
		}
	}
	mapping := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1418})
	if mapping.Security.ACL.Explicit || len(mapping.Security.ACL.Entries) != 0 {
		t.Fatal("private mapping catalog acquired PUBLIC ACL")
	}
	// Bootstrap grantor policy must retain principal identity, not a role name.
	for _, oid := range []ids.OID{1213, 6243} {
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
		if d.Owner != e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID) || d.Security.ACL.Entries[0].Grantor != d.Owner {
			t.Fatal("bootstrap owner/grantor", d.Name())
		}
	}
}

// Used by behavioral coverage with local invalid inventories, without mutating
// process-global dispatch ownership.
func validateCatalogSourceProducers(entries []pgcatalog.Registration) error {
	for i, entry := range entries {
		if entry.Produce == nil || explicitCatalogSource(entry.OID) || i > 0 && entries[i-1].OID >= entry.OID {
			return fmt.Errorf("stuntgres: invalid catalog producer ownership")
		}
	}
	return nil
}

// captureCatalogSourceLocked runs source's registered producer over view and
// roles, without the per-source policy of openCatalogSourceLocked.
func captureCatalogSourceLocked(e *core.Engine, ctx context.Context, view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	produce, ok := pgcatalog.ProducerFor(source.Address().ObjectID)
	if !ok {
		return nil, fmt.Errorf("stuntgres: unknown catalog source")
	}
	in := pgcatalog.Input{Context: ctx, Catalog: view, Roles: roles, Owner: &e.Catalog.Owner, Ref: ref, Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, SequenceBase: view.State()})}
	return core.CaptureCatalogSourceRows(in, produce)
}

// explicitCatalogSource reports the sources openCatalogSourceLocked captures
// without a registered producer: pg_locks and pg_sequences.
func explicitCatalogSource(oid ids.OID) bool { return oid == pgcatalog.LocksOID || oid == 12048 }

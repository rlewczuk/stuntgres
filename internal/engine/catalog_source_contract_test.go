package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCatalogSourceProducerCoverage(t *testing.T) {
	e := New()
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "producer_contract_handler"}, Wrapper: &fdwUnitWrapper{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, statement := range []string{
		"CREATE TABLE producer_contract_row(id INT DEFAULT 7)",
		"CREATE INDEX producer_contract_index ON producer_contract_row(id)",
		"CREATE VIEW producer_contract_view AS SELECT id FROM producer_contract_row",
		"CREATE SEQUENCE producer_contract_sequence",
		"CREATE FOREIGN DATA WRAPPER producer_contract_fdw HANDLER producer_contract_handler",
		"CREATE SERVER producer_contract_server FOREIGN DATA WRAPPER producer_contract_fdw",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER producer_contract_server OPTIONS (token 'private')",
		"CREATE FOREIGN TABLE producer_contract_foreign(id INT) SERVER producer_contract_server",
	} {
		roleExecute(t, e, op, statement)
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
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
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
		e.mu.Lock()
		source, err := e.openRelationSourceLocked(scope, ref, d.Owner, plan.RowMark{}, nil, nil)
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
		e.mu.Lock()
		e.catalog.state.SetDescriptor(unknown.Address(), &unknown)
		ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), unknown.Address())
		source, err := e.openRelationSourceLocked(scope, ref, unknown.Owner, plan.RowMark{}, nil, nil)
		want := "stuntgres: unknown catalog source"
		if virtual {
			want = "stuntgres: unknown virtual catalog view"
		}
		if source != nil || err == nil || err.Error() != want {
			t.Fatal("unknown source diagnostic", source, err)
		}
	}
}

func TestCatalogSourceInputProvenance(t *testing.T) {
	e, foreign := New(), New()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TypeOID})
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.Roles(), Owner: e.catalog.owner(), Ref: e.catalog.Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{}}
	rows, err := capturePGType(in)
	if err != nil || len(rows) == 0 {
		t.Fatal(err)
	}
	bad := in
	bad.Owner = foreign.catalog.owner()
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
	op, done := roleOperation(t, e, "postgres")
	auth, err := operationAuthorization(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.DataTypePrivilegesOID})
	mixed := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: foreign.Roles(), Owner: e.catalog.owner(), Ref: e.catalog.Domain.Ref(view.Epoch(), d.Address()), Source: d, Runtime: engineCatalogRuntime{auth: auth, database: "unit_database"}}
	if got, err := captureDataTypePrivileges(mixed); got != nil || err == nil {
		done()
		t.Fatal("mixed input/runtime roles admitted", err)
	}
	d = view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserMappingsOID})
	mixed.Ref, mixed.Source = e.catalog.Domain.Ref(view.Epoch(), d.Address()), d
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
	if err := e.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := capturePGType(in)
	if err != nil || len(again) != len(rows) || !reflect.DeepEqual(retained, rows[0]) {
		t.Fatal("coherent retained roots rejected/mutated", err)
	}
	bad = in
	bad.Catalog = e.CatalogRead()
	bad.Ref = e.catalog.Domain.Ref(bad.Catalog.Epoch(), source.Address())
	if got, err := capturePGType(bad); got != nil || err == nil {
		t.Fatal("old source paired with new generation", err)
	}
	wrapped, err := captureCatalogSourceRows(in, capturePGType)
	if err != nil || wrapped.Ref != in.Ref || len(wrapped.Anonymous) != len(rows) {
		t.Fatal("actual row wrapper ownership", err)
	}
}

func TestCatalogSourceRuntimeCapture(t *testing.T) {
	t.Run("view_table_usage_database_frontier", infoViewTableUsageCapture)
	t.Run("user_mappings_database_frontier", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		state := catalog.CloneState(f.e.catalog.state)
		server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 20300}, ObjectName: "capture_server", ObjectRevision: 1, Owner: f.owner}
		mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 20301}, ObjectName: catalog.ForeignMappingName(server.Address(), f.reader), ObjectRevision: 1, Server: server.Address(), MappedRole: f.reader}
		state.SetDescriptor(server.Address(), server)
		state.SetDescriptor(mapping.Address(), mapping)
		f.e.catalog.state = state
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
		f.e.mu.Lock()
		err = f.e.captureCommentSnapshotLocked(scope)
		f.e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		view := scope.inquiry.commentCatalog
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserMappingsOID})
		f.e.mu.Lock()
		source, err := f.e.openRelationSourceLocked(scope, view.Owner().Domain.Ref(view.Epoch(), d.Address()), d.Owner, plan.RowMark{}, nil, nil)
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
		e := engineOf(s)
		sessionSQL(t, s, "DECLARE producer_runtime CURSOR FOR SELECT i FROM unit_rows")
		sessionSQL(t, s, "SET row_security = on")
		cursor := payloadOf(s).cursors.entries["producer_runtime"]
		op := transactionOperation(t, s)
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
			e.mu.Lock()
			source, err := e.openRelationSourceLocked(scope, e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address()), d.Owner, plan.RowMark{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if oid == pgcatalog.CursorsOID {
				name, _ := source.Anonymous[0][0].Text()
				if name != "producer_runtime" || cursor.Place != exec.CursorBefore || cursor.stage != nil {
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
		e := New()
		admin, done := roleOperation(t, e, "postgres")
		defer done()
		roleExecute(t, e, admin, "CREATE ROLE producer_runtime_owner LOGIN")
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "SET SESSION AUTHORIZATION producer_runtime_owner")
		sessionSQL(t, s, "BEGIN")
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
			e.mu.Lock()
			defer e.mu.Unlock()
			auth, err := e.authorizationLocked(admin)
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
			if err := publicationOperationLocked(e, admin); err != nil {
				return err
			}
			e.installOperationRolesLocked(admin, next)
			return nil
		}(); err != nil {
			t.Fatal(err)
		}
		if owner, ok := retained.Lookup("producer_runtime_owner"); !ok || owner.Name != "producer_runtime_owner" {
			t.Fatal("published rename mutated retained input roles")
		}
		view := e.CatalogRead()
		d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12001})
		in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: retained, Owner: e.catalog.owner(), Source: d, Runtime: engineCatalogRuntime{engine: e}}
		e.mu.Lock()
		expectedXID := e.visibility.AssignedXIDLocked(e.prepared.Lookup("producer_runtime_gid").Ref)
		rows, err := capturePreparedSource(in)
		e.mu.Unlock()
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
		e.mu.Lock()
		got, err := capturePreparedSource(in)
		e.mu.Unlock()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("prepared cancellation published prefix", err)
		}
	})
}

func TestCatalogSourceSpecMetadata(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	stableAttributes := map[ids.OID][]int32{
		1255: {1, 2, 3, 4, 17, 19, 15, 13, 16, 12, 23},
		2610: {1, 2, 5, 7, 11, 13, 14, 6},
	}
	for _, descriptor := range view.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.CatalogRelation {
			continue
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
		if d.Owner != e.roles.domain.Principal(authorization.BootstrapPrincipalID) || d.Security.ACL.Entries[0].Grantor != d.Owner {
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
func (e *Engine) captureCatalogSourceLocked(ctx context.Context, view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	produce, ok := pgcatalog.ProducerFor(source.Address().ObjectID)
	if !ok {
		return nil, fmt.Errorf("stuntgres: unknown catalog source")
	}
	in := pgcatalog.Input{Context: ctx, Catalog: view, Roles: roles, Owner: e.catalog.owner(), Ref: ref, Source: source, Runtime: engineCatalogRuntime{engine: e, sequenceBase: view.State()}}
	return captureCatalogSourceRows(in, produce)
}

// explicitCatalogSource reports the sources openCatalogSourceLocked captures
// without a registered producer: pg_locks and pg_sequences.
func explicitCatalogSource(oid ids.OID) bool { return oid == pgcatalog.LocksOID || oid == 12048 }

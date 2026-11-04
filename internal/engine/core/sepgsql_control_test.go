package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
)

func TestSepgsqlSimulationControls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := sepgsqlPhaseEngine(t)
	physical := enginetest.StartSession(t, e, nil)
	authority, err := e.CaptureControlSepgsql(ctx, physical, nil)
	if err != nil || authority == nil {
		t.Fatal("genuine physical control capture", authority, err)
	}
	if err = e.ValidateControlSepgsql(authority); err != nil {
		t.Fatal(err)
	}
	before := e.CatalogRead().State()
	if err = engine.ResetWithAuthority(ctx, e, nil); err == nil || e.CatalogRead().State() != before {
		t.Fatal("missing detached origin was treated as trusted internal control", err)
	}
	foreign := sepgsqlPhaseEngine(t)
	if err = foreign.ValidateControlSepgsql(authority); err == nil {
		t.Fatal("foreign owner adopted detached capture")
	}
	if err = engine.ResetWithAuthority(ctx, foreign, authority); err == nil {
		t.Fatal("foreign replacement adopted capture")
	}
	if e.CatalogRead().State() != before {
		t.Fatal("foreign/missing origin changed source catalog")
	}

	// A genuine host-internal control remains trusted without fabricating a
	// client subject. Its publication invalidates the earlier detached target.
	if err = engine.Reset(ctx, e); err != nil {
		t.Fatal("internal replacement acquired client MAC requirement", err)
	}
	installed := e.CatalogRead().State()
	if installed == before {
		t.Fatal("internal replacement did not publish its genuine root")
	}
	err = engine.ResetWithAuthority(ctx, e, authority)
	if !errors.Is(err, ids.ErrCatalogReplaced) || e.CatalogRead().State() != installed {
		t.Fatal("retired original control root was silently recaptured", err)
	}

	assertLabels := func(t *testing.T, state *catalog.State, object ids.ObjectAddress, attributes ...int32) {
		t.Helper()
		for _, sub := range attributes {
			address := object
			address.SubID = sub
			record := state.SecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
			if record == nil || record.Label != "P" || record.PolicyID != "phase-owner" || record.PolicyVersion != "1" {
				t.Fatalf("genuine TEMP create label at %v: %#v", address, record)
			}
		}
	}
	assertRetired := func(t *testing.T, state *catalog.State, address ids.ObjectAddress) {
		t.Helper()
		if state.Get(address) != nil {
			t.Fatal("retired TEMP descriptor survived", address)
		}
		for key := range state.SecurityLabels() {
			if key.Object.ClassID == address.ClassID && key.Object.ObjectID == address.ObjectID {
				t.Fatal("retired TEMP label survived", key)
			}
		}
	}
	t.Run("fresh_temporary_namespace_requires_schema_create", func(t *testing.T) {
		e := sepgsqlPhaseEngine(t)
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
		before := e.CatalogRead()
		stores := len(e.Storage.Relations)
		func() {
			op, err := s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			// transformCreateStmt's namespace-resolution callback supplies the
			// created relation's one-based location, including implicit schema hooks.
			enginetest.RoleErrorPosition(t, e, op, "CREATE TEMP TABLE control_denied(v integer)", "42501", "SELinux: security policy violation", "", "19")
		}()
		if e.CatalogRead().State() != before.State() || len(e.Storage.Relations) != stores ||
			s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil {
			t.Fatal("schema CREATE denial published TEMP state")
		}
		for _, ns := range e.CatalogRead().TemporaryNamespaces() {
			if ns.Temporary == s.SessionToken {
				t.Fatal("denied first TEMP creation retained a namespace")
			}
		}
	})
	for _, denyToast := range []bool{false, true} {
		t.Run(map[bool]string{false: "normalized_temporary_creation", true: "late_toast_creation_denial"}[denyToast], func(t *testing.T) {
			e := engine.New()
			var mu sync.Mutex
			var events []sepgsql.AuditEvent
			rules := []sepgsql.Rule{
				{Source: "R", Target: "P", Class: "db_database", Allow: []string{"access", "getattr"}},
				{Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search"}},
				{Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute"}},
				{Source: "R", Target: "N", Class: "db_schema", Allow: []string{"create", "search", "add_name", "remove_name"}},
				{Source: "R", Target: "T", Class: "db_schema", Allow: []string{"create", "search"}},
				{Source: "R", Target: "N", Class: "db_table", Allow: []string{"create"}},
				{Source: "R", Target: "N", Class: "db_column", Allow: []string{"create"}},
			}
			if denyToast {
				rules = append(rules, sepgsql.Rule{Source: "R", Target: "T", Class: "db_schema", Deny: []string{"create"}})
			}
			runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Audit: func(event sepgsql.AuditEvent) {
				mu.Lock()
				events = append(events, event)
				mu.Unlock()
			}, Policy: sepgsql.Policy{
				ID: "temporary-creation", Version: "1", Contexts: []string{"R", "P", "N", "T", "U"}, InitialContext: "R", UnlabeledContext: "U",
				DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
				Rules:       rules,
				CreateTransitions: []sepgsql.CreateTransition{
					{Source: "R", Parent: "P", Class: "db_schema", Name: "pg_temp", Result: "N"},
					{Source: "R", Parent: "P", Class: "db_schema", Name: "pg_toast_temp", Result: "T"},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			err = e.InstallSepgsqlSimulation(runtime, true)
			if err == nil {
				e.InitializeSepgsqlSeedLabels()
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			s := enginetest.StartSession(t, e, nil)
			before := e.CatalogRead()
			stores := len(e.Storage.Relations)
			if denyToast {
				func() {
					op, err := s.Acquire(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					enginetest.RoleErrorPosition(t, e, op, "CREATE TEMP TABLE normalized_temp(v integer)", "42501", "SELinux: security policy violation", "", "19")
				}()
				if e.CatalogRead().State() != before.State() || len(e.Storage.Relations) != stores ||
					s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil {
					t.Fatal("late toast CREATE denial partially published namespace/table/labels")
				}
			} else {
				enginetest.SessionSQL(t, s, "CREATE TEMP TABLE normalized_temp(v integer)")
				toast := enginetest.AssertTemporaryNamespacePair(t, e, s.SessionToken, s.TemporaryNamespace)
				read := e.CatalogRead()
				table := read.State().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "normalized_temp"})
				for address, want := range map[ids.ObjectAddress]string{s.TemporaryNamespace: "N", toast: "T", table: "N"} {
					record := read.State().SecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
					if address == (ids.ObjectAddress{}) || record == nil || record.Label != want || record.PolicyID != "temporary-creation" {
						t.Fatal("normalized namespace transition/default inheritance", address, record, want)
					}
				}
				column := table
				column.SubID = 1
				if record := read.State().SecurityLabel(catalog.SecurityLabelKey{Object: column, Provider: "selinux"}); record == nil || record.Label != "N" {
					t.Fatal("table column did not inherit actual normalized namespace context", record)
				}
			}
			mu.Lock()
			var creates []sepgsql.AuditEvent
			for _, event := range events {
				if event.Class == "db_schema" && slices.Contains(event.Required, "create") {
					creates = append(creates, event)
				}
			}
			mu.Unlock()
			if len(creates) != 2 || creates[0].Name != "pg_temp" || creates[0].Target != "N" || len(creates[0].Denied) != 0 ||
				creates[1].Name != "pg_toast_temp" || creates[1].Target != "T" ||
				(denyToast && !slices.Equal(creates[1].Denied, []string{"create"})) || (!denyToast && len(creates[1].Denied) != 0) {
				t.Fatal("TEMP source order, normalized CREATE audit names or denied bits", creates)
			}
		})
	}
	t.Run("temporary_namespace_creation_reuse_and_physical_cleanup", func(t *testing.T) {
		e := sepgsqlPhaseEngine(t)
		s := enginetest.StartSession(t, e, nil)
		prior := e.CatalogRead()
		priorLabels := prior.State().SecurityLabelRoot()
		enginetest.SessionSQL(t, s, "CREATE TEMP TABLE control_temp(a integer,b text)")
		namespace := s.TemporaryNamespace
		toast := enginetest.AssertTemporaryNamespacePair(t, e, s.SessionToken, namespace)
		first := e.CatalogRead()
		firstLabels := first.State().SecurityLabelRoot()
		name := ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "control_temp"}
		table := first.State().NameAddress(name)
		if table == (ids.ObjectAddress{}) || prior.Get(namespace) != nil || prior.Get(table) != nil || prior.State().SecurityLabelRoot() != priorLabels {
			t.Fatal("TEMP creation failed or mutated the retained entry catalog")
		}
		assertLabels(t, first.State(), namespace, 0)
		assertLabels(t, first.State(), toast, 0)
		assertLabels(t, first.State(), table, -6, -5, -4, -3, -2, -1, 0, 1, 2)
		enginetest.SessionSQL(t, s, "INSERT INTO control_temp VALUES(1,'kept')")
		enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
		enginetest.SessionSQL(t, s, "CREATE TEMP TABLE control_temp_reuse(v integer)")
		reused := e.CatalogRead()
		secondName := ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "control_temp_reuse"}
		second := reused.State().NameAddress(secondName)
		if s.TemporaryNamespace != namespace || reused.Get(namespace) != first.Get(namespace) ||
			first.State().SecurityLabelRoot() != firstLabels ||
			reused.Get(toast) != first.Get(toast) || first.State().NameAddress(secondName) != (ids.ObjectAddress{}) ||
			reused.State().SecurityLabel(catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}) != first.State().SecurityLabel(catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}) {
			t.Fatal("second TEMP creation replaced namespace authority or mutated a retained catalog")
		}
		assertLabels(t, reused.State(), second, -6, -5, -4, -3, -2, -1, 0, 1)
		enginetest.TransactionRows(t, s, "SELECT a FROM control_temp", 1)
		peer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, peer, "CREATE TEMP TABLE control_peer(v integer)")
		peerRead := e.CatalogRead()
		peerName := ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "control_peer"}
		peerTable := peerRead.State().NameAddress(peerName)
		peerStore := e.Storage.Relations[peerTable]
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		retired := e.CatalogRead()
		for _, address := range []ids.ObjectAddress{table, second} {
			assertRetired(t, retired.State(), address)
			if e.Storage.Relations[address] != nil {
				t.Fatal("physical cleanup retained TEMP heap", address)
			}
		}
		if retired.State().NameAddress(name) != (ids.ObjectAddress{}) || retired.State().NameAddress(secondName) != (ids.ObjectAddress{}) ||
			s.TemporaryNamespace != (ids.ObjectAddress{}) || retired.Get(namespace) != first.Get(namespace) ||
			retired.Get(toast) != first.Get(toast) || retired.Get(peerTable) != peerRead.Get(peerTable) ||
			e.Storage.Relations[peerTable] != peerStore || reused.Get(table) == nil || reused.Get(second) == nil {
			t.Fatal("cleanup leaked names/intent or changed retained/peer identities")
		}
		assertLabels(t, retired.State(), namespace, 0)
		assertLabels(t, retired.State(), toast, 0)
		assertLabels(t, retired.State(), peerTable, 0, 1)
		assertLabels(t, reused.State(), table, 0, 1, 2)
	})
	for _, source := range []string{"collation", "inquiry"} {
		t.Run("temporary_namespace_"+source+"_source", func(t *testing.T) {
			e := sepgsqlPhaseEngine(t)
			s := enginetest.StartSession(t, e, &schemaDefaults{path: "pg_temp,public"})
			before := e.CatalogRead()
			if source == "collation" {
				enginetest.SessionSQL(t, s, `CREATE COLLATION pg_temp.control_collation FROM pg_catalog."C"`)
			} else {
				result := enginetest.SessionSQL(t, s, "SELECT current_schema()")
				rows := enginetest.Tuples(result)
				if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != e.CatalogRead().MustNamespace(s.TemporaryNamespace).Name() {
					t.Fatal("demanded namespace inquiry did not publish its authentic namespace", rows)
				}
			}
			namespace := s.TemporaryNamespace
			toast := enginetest.AssertTemporaryNamespacePair(t, e, s.SessionToken, namespace)
			created := e.CatalogRead()
			assertLabels(t, created.State(), namespace, 0)
			assertLabels(t, created.State(), toast, 0)
			database := e.Catalog.Owner.DatabaseAddress()
			key := catalog.SecurityLabelKey{Object: database, Provider: "selinux"}
			if before.Get(namespace) != nil || before.Get(toast) != nil || created.Get(database) != before.Get(database) ||
				created.State().SecurityLabel(key) != before.State().SecurityLabel(key) {
				t.Fatal("TEMP source transfer changed database predecessor or retained entry read")
			}
			enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
			enginetest.SessionSQL(t, s, "SELECT current_schema()")
			reused := e.CatalogRead()
			if s.TemporaryNamespace != namespace || reused.Get(namespace) != created.Get(namespace) ||
				reused.State().SecurityLabel(catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}) != created.State().SecurityLabel(catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}) {
				t.Fatal("namespace inquiry replayed CREATE or lost actual label authority")
			}
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(t.Context()); err != nil {
				t.Fatal(err)
			}
			cleaned := e.CatalogRead()
			if cleaned.Get(namespace) != created.Get(namespace) || cleaned.Get(toast) != created.Get(toast) {
				t.Fatal("physical cleanup deleted genuine namespace shells")
			}
			assertLabels(t, cleaned.State(), namespace, 0)
			assertLabels(t, cleaned.State(), toast, 0)
			if source == "collation" {
				name := ids.NameKey{ClassID: ids.CollationClassID, Namespace: namespace, Name: "control_collation"}
				address := created.State().NameAddress(name)
				if address == (ids.ObjectAddress{}) || cleaned.Get(address) != nil || created.Get(address) == nil || cleaned.State().NameAddress(name) != (ids.ObjectAddress{}) {
					t.Fatal("migrated collation transfer/retirement lost real identity or retained read")
				}
			}
		})
	}
	for _, rollback := range []string{"transaction", "savepoint"} {
		t.Run("temporary_namespace_"+rollback+"_rollback", func(t *testing.T) {
			e := sepgsqlPhaseEngine(t)
			s := enginetest.StartSession(t, e, nil)
			committed := e.CatalogRead()
			enginetest.SessionSQL(t, s, "BEGIN")
			if rollback == "savepoint" {
				enginetest.SessionSQL(t, s, "SAVEPOINT temp_intent")
			}
			enginetest.SessionSQL(t, s, "CREATE TEMP TABLE control_abandoned(v integer)")
			enginetest.SessionSQL(t, s, "INSERT INTO control_abandoned VALUES(7)")
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
			var namespace, toast ids.ObjectAddress
			for address, ns := range private.TemporaryNamespaces() {
				if ns.Temporary == s.SessionToken {
					if ns.TemporaryToast {
						toast = address
					} else {
						namespace = address
					}
				}
			}
			name := ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "control_abandoned"}
			table := private.State().NameAddress(name)
			if namespace == (ids.ObjectAddress{}) || toast == (ids.ObjectAddress{}) || table == (ids.ObjectAddress{}) ||
				e.CatalogRead().State() != committed.State() {
				t.Fatal("initial transactional TEMP creation was not private")
			}
			assertLabels(t, private.State(), namespace, 0)
			assertLabels(t, private.State(), toast, 0)
			assertLabels(t, private.State(), table, 0, 1)
			if rollback == "savepoint" {
				enginetest.SessionSQL(t, s, "ROLLBACK TO temp_intent")
				enginetest.SessionSQL(t, s, "RELEASE temp_intent")
			} else {
				enginetest.SessionSQL(t, s, "ROLLBACK")
			}
			if e.CatalogRead().State() != committed.State() || s.TemporaryNamespace != (ids.ObjectAddress{}) {
				t.Fatal("rollback published initial TEMP namespace intent")
			}
			for _, address := range []ids.ObjectAddress{namespace, toast, table} {
				assertRetired(t, e.CatalogRead().State(), address)
			}
			enginetest.SessionSQL(t, s, "CREATE TEMP TABLE control_after_rollback(v integer)")
			if rollback == "savepoint" {
				enginetest.SessionSQL(t, s, "COMMIT")
			}
			after := e.CatalogRead()
			if s.TemporaryNamespace == namespace || s.TemporaryNamespace == (ids.ObjectAddress{}) ||
				after.State().NameAddress(name) != (ids.ObjectAddress{}) || private.Get(namespace) == nil || private.Get(table) == nil {
				t.Fatal("rolled-back intent was reused or retained catalog was mutated")
			}
			for _, address := range []ids.ObjectAddress{namespace, toast, table} {
				assertRetired(t, after.State(), address)
			}
			assertLabels(t, after.State(), s.TemporaryNamespace, 0)
			createdName := ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "control_after_rollback"}
			created := after.State().NameAddress(createdName)
			assertLabels(t, after.State(), created, 0, 1)
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(ctx); err != nil {
				t.Fatal(err)
			}
			assertRetired(t, e.CatalogRead().State(), created)
			if e.Storage.Relations[created] != nil || s.TemporaryNamespace != (ids.ObjectAddress{}) {
				t.Fatal("post-rollback physical cleanup retained TEMP resources")
			}
		})
	}
	t.Run("sequence_replay_preserves_actual_and_absent_associations", func(t *testing.T) {
		// The portable simulation marker carries the actual selected database
		// name. Use the named native-control fixture, not the anonymous phase
		// engine, so the real Dump image satisfies its requirement contract.
		e := engine.NewWithSettings(nil, "sequence_control", false)
		sepgsqlRestoreconInstall(t, e, sepgsqlRestoreconPolicy(t, nil, false))
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{
			"CREATE USER sequence_control_reader",
			"CREATE SEQUENCE sequence_default START 5 RESTART 9 CACHE 3",
			"CREATE UNLOGGED SEQUENCE sequence_relabeled START 7 CACHE 2",
			"CREATE SEQUENCE sequence_absent START 11 CACHE 1",
			"CREATE TABLE sequence_serial(id serial)",
			"CREATE TABLE sequence_identity(id integer GENERATED ALWAYS AS IDENTITY)",
			"CREATE TABLE sequence_added(id integer NOT NULL)",
			"ALTER TABLE sequence_added ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY",
			"GRANT SELECT, USAGE ON SEQUENCE sequence_default TO sequence_control_reader",
			"SECURITY LABEL ON SEQUENCE sequence_relabeled IS 'S'",
			"SECURITY LABEL ON SEQUENCE sequence_absent IS NULL",
			"CREATE TEMP SEQUENCE sequence_control_temp START 23",
		} {
			enginetest.SessionSQL(t, s, q)
		}
		type savedSequence struct {
			options        catalog.SequenceOptions
			acl            authorization.ACLValue
			label          *catalog.SecurityLabelRecord
			last           int64
			called         bool
			ownedName      string
			ownedAttribute int32
		}
		saved := make(map[string]savedSequence)
		source := e.CatalogRead()
		for address, descriptor := range source.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || d.Sequence == nil || d.Namespace() == s.TemporaryNamespace {
				continue
			}
			result := enginetest.SessionSQL(t, s, "SELECT nextval('"+d.Name()+"')")
			result.Rows.Close()
			// A transactional restart is the promised uncalled generator state.
			if d.Name() == "sequence_absent" {
				enginetest.SessionSQL(t, s, "ALTER SEQUENCE sequence_absent RESTART 19")
			}
			current := e.CatalogRead().MustRelation(address)
			generator := e.Storage.Sequences[address]
			want := savedSequence{options: *current.Sequence, acl: current.Security.ACL, label: e.CatalogRead().State().SecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"}), last: generator.Last, called: generator.Called}
			label := "P"
			switch current.Name() {
			case "sequence_relabeled":
				label = "S"
			case "sequence_absent":
				label = ""
			}
			if current.Owner != e.Roles().LookupRef("postgres") ||
				(label == "" && want.label != nil) ||
				(label != "" && (want.label == nil || want.label.Label != label ||
					want.label.PolicyID != e.Sepgsql.ID() || want.label.PolicyVersion != e.Sepgsql.Version())) {
				t.Fatal("source sequence owner/default/relabeled/NULL association", current.Name(), current.Owner, want.label)
			}
			if owned := current.Sequence.Owned; owned != (ids.ObjectAddress{}) {
				want.ownedAttribute = owned.SubID
				owned.SubID = 0
				want.ownedName = source.Get(owned).Name()
			}
			saved[current.Name()] = want
		}
		if len(saved) != 6 {
			t.Fatal("explicit/serial/identity sequence cohort", saved)
		}
		var image bytes.Buffer
		if err := engine.Dump(t.Context(), e, &image); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(image.String(), "sequence_control_temp") {
			t.Fatal("TEMP sequence serialized as durable state")
		}
		temp := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "sequence_control_temp"})
		tempObject, tempGenerator := e.CatalogRead().Get(temp), e.Storage.Sequences[temp]
		if err := engine.Reset(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		if err := engine.Restore(t.Context(), e, bytes.NewReader(image.Bytes())); err != nil {
			t.Fatal(err)
		}
		restored := e.CatalogRead()
		for name, want := range saved {
			address := restored.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
			d := restored.MustRelation(address)
			options := *d.Sequence
			options.Owned = want.options.Owned // Addresses are checked relationally below.
			generator := e.Storage.Sequences[address]
			key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
			if d.Owner != e.Roles().LookupRef("postgres") ||
				!reflect.DeepEqual(options, want.options) || !reflect.DeepEqual(d.Security.ACL, want.acl) ||
				!reflect.DeepEqual(restored.State().SecurityLabel(key), want.label) || generator == nil ||
				generator.Last != want.last || generator.Called != want.called {
				t.Fatal("trusted replay recomputed sequence label/options/ACL/generator", name, d, restored.State().SecurityLabel(key), generator, want)
			}
			if owned := d.Sequence.Owned; owned != (ids.ObjectAddress{}) {
				if owned.SubID != want.ownedAttribute {
					t.Fatal("owned attribute remap", name, owned)
				}
				owned.SubID = 0
				if restored.Get(owned) == nil || restored.Get(owned).Name() != want.ownedName {
					t.Fatal("owned table identity remap", name, owned)
				}
			} else if want.ownedName != "" {
				t.Fatal("owned sequence lost its table link", name)
			}
			for label := range restored.State().SecurityLabels() {
				if label.Object.ClassID == address.ClassID && label.Object.ObjectID == address.ObjectID && label.Object.SubID != 0 {
					t.Fatal("replay invented sequence column labels", label)
				}
			}
		}
		if restored.Get(temp) != tempObject || e.Storage.Sequences[temp] != tempGenerator {
			t.Fatal("persistent replacement changed genuine live TEMP sequence")
		}
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(t.Context()); err != nil {
			t.Fatal(err)
		}
		if e.CatalogRead().Get(temp) != nil || e.Storage.Sequences[temp] != nil ||
			e.CatalogRead().State().SecurityLabel(catalog.SecurityLabelKey{Object: temp, Provider: "selinux"}) != nil {
			t.Fatal("physical cleanup retained restored-session TEMP sequence")
		}
	})
}

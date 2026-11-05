package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/parser"
)

func TestDatabaseDropPublication(t *testing.T) {
	t.Run("retained_database_qualified_and_disjoint_settings", databaseDropSettingsFence)
	t.Run("revision_exhaustion_preserves_all_roots_and_victims", databaseDropRevisionFailure)
	t.Run("actual_backing_and_immutable_shared_authority", func(t *testing.T) {
		e, target, admin := databaseDropOwnerFixture(t)
		for _, runtime := range []*core.Engine{e, target} {
			enginetest.Run(t, runtime, "CREATE TABLE drop_backing(v integer)")
			enginetest.Run(t, runtime, "INSERT INTO drop_backing VALUES(11)")
			enginetest.Run(t, runtime, "CREATE INDEX drop_backing_key ON drop_backing(v)")
			enginetest.Run(t, runtime, "CREATE SEQUENCE drop_generator START 41")
		}
		enginetest.Run(t, e, "CREATE ROLE drop_settings_owner")
		enginetest.Run(t, e, "ALTER ROLE drop_settings_owner SET statement_timeout='5s'")
		enginetest.Run(t, e, "ALTER ROLE drop_settings_owner IN DATABASE drop_owner_target SET lock_timeout='20ms'")
		enginetest.Run(t, e, "ALTER DATABASE drop_owner_target SET statement_timeout='30ms'")
		enginetest.Run(t, e, "SELECT pg_replication_origin_create('drop_shared_origin')")
		root, peerRoot := target.CatalogRead(), e.CatalogRead()
		sharedDescriptors := make(map[ids.ObjectAddress]catalog.Descriptor)
		for address, descriptor := range peerRoot.Descriptors() {
			switch address.ClassID {
			case ids.ParameterClassID, ids.TablespaceClassID, ids.ReplicationOriginClassID:
				sharedDescriptors[address] = descriptor
			}
		}
		targetRef, peerRef := enginetest.RoleTableRef(t, target, "drop_backing"), enginetest.RoleTableRef(t, e, "drop_backing")
		stored, peerStored := target.Storage.Relations[targetRef.Address], e.Storage.Relations[peerRef.Address]
		if stored == nil || len(stored.Versions) != 1 || len(target.Storage.IndexRoots) == 0 || len(target.Storage.Sequences) == 0 {
			t.Fatal("fixture lacks genuine physical backing")
		}
		roles := e.RoleStore.Published
		global := access.RoleSettingKey{Role: e.Roles().LookupRef("drop_settings_owner")}
		globalRow := roles.Settings.Lookup(global)
		if len(globalRow) != 1 {
			t.Fatal("missing immutable default row")
		}
		address := target.Catalog.DatabaseAddress()
		oid, nextOID, domain := address.ObjectID, e.Cluster.NextOID, target.Catalog.Domain
		locks, statements, defaultTarget := e.Locks, e.Cluster.StatementStatistics, e.Cluster.DefaultTarget
		rowFrontier, versionFrontier := target.Storage.RowFrontier(), target.Storage.VersionFrontier()
		visibility, assigned := target.Visibility.VisibilityXID(), target.Visibility.LastAssignedXID()
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(false)); err != nil {
			t.Fatal(err)
		}
		if len(target.Storage.Relations) != 0 || len(target.Storage.IndexRoots) != 0 || len(target.Storage.Sequences) != 0 || len(target.Storage.RelationMetadata) != 0 || len(target.Visibility.Entries) != 0 || len(target.Visibility.History) != 0 {
			t.Fatal("retained runtime owns retired local backing")
		}
		if target.Storage.RowFrontier() != rowFrontier || target.Storage.VersionFrontier() != versionFrontier || target.Visibility.VisibilityXID() != visibility || target.Visibility.LastAssignedXID() != assigned {
			t.Fatal("retirement rewound identities")
		}
		if target.Catalog.Domain != domain || e.Locks != locks || e.Cluster.StatementStatistics != statements || e.Cluster.DefaultTarget != defaultTarget || e.Cluster.NextOID != nextOID {
			t.Fatal("DROP replaced shared authority")
		}
		if e.Storage.Relations[peerRef.Address] != peerStored || peerRoot.Get(peerRef.Address) != e.CatalogRead().Get(peerRef.Address) {
			t.Fatal("DROP recopied peer local backing")
		}
		for address, descriptor := range sharedDescriptors {
			if e.CatalogRead().Get(address) != descriptor || target.CatalogRead().Get(address) != descriptor {
				t.Fatal("retirement replaced unrelated shared descriptor backing", address)
			}
		}
		if e.CatalogRead().State().ParameterIncarnation() != peerRoot.State().ParameterIncarnation() {
			t.Fatal("retirement changed parameter incarnation")
		}
		if _, ok := root.Database(address); !ok || root.Get(targetRef.Address) == nil || len(stored.Versions) != 1 {
			t.Fatal("retirement mutated detached catalog/storage")
		}
		if _, ok := target.CatalogRead().Database(address); ok || target.CatalogRead().Get(targetRef.Address) != nil {
			t.Fatal("tombstone retains local definitions")
		}
		if !e.RoleStore.Published.SameAuthority(roles) {
			t.Fatal("default pruning replaced principal graph lineage")
		}
		retained := e.RoleStore.Published.Settings.Lookup(global)
		if len(retained) != 1 || &retained[0] != &globalRow[0] {
			t.Fatal("unrelated default backing was recopied")
		}
		for key := range e.RoleStore.Published.Settings.Rows() {
			if key.Database == address {
				t.Fatal("database-qualified defaults survived")
			}
		}
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, &parser.CreateDatabaseStmt{Name: target.DatabaseName}); err != nil {
			t.Fatal(err)
		}
		fresh, err := e.SelectDatabase(t.Context(), target.DatabaseName)
		if err != nil {
			t.Fatal(err)
		}
		if fresh == target || fresh.Catalog.Domain == domain || fresh.Catalog.DatabaseAddress().ObjectID == oid || fresh.CatalogRead().Get(targetRef.Address) != nil {
			t.Fatal("recreation adopted retired identity/backing")
		}
		// The ordinary publisher must still reject an accidentally missing live DB.
		missing := catalog.CloneState(e.CatalogRead().State())
		missing.RemoveDescriptor(fresh.Catalog.DatabaseAddress())
		e.Mu.Lock()
		_, err = e.PrepareDatabasePublicationLocked(missing)
		e.Mu.Unlock()
		if err == nil || err.Error() != "stuntgres: lost registered database authority" {
			t.Fatal("DROP weakened general publication invariant", err)
		}
	})
	t.Run("default_tombstone_receives_later_shared_publications", func(t *testing.T) {
		e, peer, admin := databaseDropOwnerFixture(t)
		peerOwner := enginetest.StartSession(t, peer, nil)
		databaseAlterEnd(t, admin)
		if _, err := databaseDropOwnerExecute(t.Context(), peer, peerOwner, &parser.DropDatabaseStmt{Name: e.DatabaseName}); err != nil {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, peerOwner, "CREATE ROLE drop_after_default LOGIN")
		if _, err := e.ResolveLogin("drop_after_default"); err != nil {
			t.Fatal("connector root lost cluster principal authority", err)
		}
		if _, err := databaseDropOwnerExecute(t.Context(), peer, peerOwner, &parser.CreateDatabaseStmt{Name: "drop_after_shared"}); err != nil {
			t.Fatal(err)
		}
		address := peer.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: "drop_after_shared"})
		if e.CatalogRead().Get(address) != peer.CatalogRead().Get(address) || e.CatalogRead().Get(e.Catalog.DatabaseAddress()) != nil {
			t.Fatal("default authority holder fell out of shared projection")
		}
		if a, err := e.AdmitDatabase(t.Context(), peerOwner.CountedAuthenticated, enginetest.NextSessionID(), core.DatabaseSelector{}); a != nil || err == nil {
			if a != nil {
				a.Release()
			}
			t.Fatal("default tombstone became selectable")
		}
	})
	t.Run("real_MAC_precedence_and_label_retirement", func(t *testing.T) {
		e := engine.New()
		policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{ID: "database-drop-owner", Version: "1", Contexts: []string{"A", "P", "D"}, InitialContext: "A", UnlabeledContext: "P", Rules: []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}, {Source: "A", Target: "D", Class: "db_database", Deny: []string{"drop"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		err = e.InstallSepgsqlSimulation(policy, true)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		admin := enginetest.StartSession(t, e, nil)
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, &parser.CreateDatabaseStmt{Name: "drop_owner_target"}); err != nil {
			t.Fatal(err)
		}
		target, err := e.SelectDatabase(t.Context(), "drop_owner_target")
		if err != nil {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, admin, "SECURITY LABEL FOR selinux ON DATABASE drop_owner_target IS 'D'")
		enginetest.SessionSQL(t, admin, "ALTER DATABASE drop_owner_target IS_TEMPLATE true")
		old := e.CatalogRead()
		key := catalog.SecurityLabelKey{Object: target.Catalog.DatabaseAddress(), Provider: "selinux"}
		label := old.State().SecurityLabel(key)
		if label == nil || label.Label != "D" {
			t.Fatal("missing genuine database label")
		}
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(false)); err == nil || err.Error() != "stuntgres: SELinux: security policy violation" {
			t.Fatal("MAC did not precede template refusal", err)
		}
		if e.CatalogRead().State() != old.State() {
			t.Fatal("MAC refusal changed shared roots")
		}
		enginetest.SessionSQL(t, admin, "ALTER DATABASE drop_owner_target IS_TEMPLATE false")
		enginetest.SessionSQL(t, admin, "SECURITY LABEL FOR selinux ON DATABASE drop_owner_target IS 'P'")
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(false)); err != nil {
			t.Fatal(err)
		}
		for store := range e.Cluster.Catalogs {
			if store.State.SecurityLabel(key) != nil || store.State.Get(key.Object) != nil {
				t.Fatal("database label/descriptor survived shared retirement")
			}
		}
		if old.State().SecurityLabel(key) != label {
			t.Fatal("label retirement mutated old immutable root")
		}
	})
}

package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

// Inspect the actual provider output and candidate observations, not just the
// rendered pg_init_privs rows covered by the independent reference scenario.
func TestPGInitPrivsProviderDefaults(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	run := func(sql string) { enginetest.RoleExecute(t, e, op, sql) }
	for _, sql := range []string{
		"CREATE ROLE ip_default_reader", "CREATE ROLE ip_default_scoped",
		"CREATE SCHEMA ip_defaults",
		"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ip_default_reader WITH GRANT OPTION",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA ip_defaults GRANT EXECUTE ON FUNCTIONS TO ip_default_scoped WITH GRANT OPTION",
	} {
		run(sql)
	}
	base := e.CatalogRead()
	namespace := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ip_defaults"})
	reader, _ := e.Roles().Lookup("ip_default_reader")
	scoped, _ := e.Roles().Lookup("ip_default_scoped")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	host := &schemacmds.ExtensionHost{Engine: e, Op: op, Auth: auth}
	stage, err := schemacmds.StageOf(host.BeginStage(namespace))
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	acl, err := host.RoutineCreationACLWithoutPublic(auth.State.CurrentUser, namespace)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	owner := auth.State.CurrentUser
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: scoped.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
	}}
	if err != nil || !reflect.DeepEqual(acl, want) {
		t.Fatal("selective script revoke discarded creation defaults", acl, err)
	}
	globalKey := catalog.DefaultACLKey{Creator: owner, Class: authorization.DefaultRoutines}
	scopedKey := catalog.DefaultACLKey{Creator: owner, Schema: namespace, Class: authorization.DefaultRoutines}
	globalRecord := base.State().DefaultACL(globalKey)
	scopedRecord := base.State().DefaultACL(scopedKey)
	if globalRecord == nil || scopedRecord == nil {
		t.Fatal("missing independent creation-default fixtures")
	}
	globalEntries := slices.Clone(globalRecord.ACL.Entries)
	scopedEntries := slices.Clone(scopedRecord.ACL.Entries)
	observed := 0
	for key, record := range stage.DefaultReads() {
		if key != globalKey && key != scopedKey || record != base.State().DefaultACL(key) {
			t.Fatal("provider creation read wrong default authority", key, record)
		}
		observed++
	}
	if observed != 2 || !stage.DefaultRecordsUnchanged(base.State()) {
		t.Fatal("provider failed to observe both global and schema defaults", observed)
	}
	e.Mu.Lock()
	rawACL, rawErr := host.RoutineCreationACLWithoutPublic(reader.Ref, namespace)
	e.Mu.Unlock()
	rawWant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ExecutePrivilege},
	}}
	if rawErr != nil || !reflect.DeepEqual(rawACL, rawWant) {
		t.Fatal("raw NULL creation defaults did not materialize selectively", rawACL, rawErr)
	}
	absent := 0
	for key, record := range stage.DefaultReads() {
		if key.Creator == reader.Ref {
			if record != nil || key.Class != authorization.DefaultRoutines || key.Schema != (ids.ObjectAddress{}) && key.Schema != namespace {
				t.Fatal("raw NULL creation defaults lost absence observation", key, record)
			}
			absent++
		}
	}
	if absent != 2 {
		t.Fatal("raw NULL creation missed global or schema absence", absent)
	}
	run("CREATE EXTENSION amcheck SCHEMA ip_defaults")
	installed := e.CatalogRead()
	ext := installed.MustExtension(installed.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
	count := 0
	for _, address := range ext.Members {
		if address.ClassID != ids.ProcedureClassID {
			continue
		}
		proc := installed.MustProcedure(address)
		record := installed.State().InitialPrivilege(address)
		if record == nil || record.PrivType != 'e' || !reflect.DeepEqual(proc.ACL, want) || !reflect.DeepEqual(record.ACL, want) {
			t.Fatal("provider snapshot omitted a default grantee/option", proc.Name(), proc.ACL, record)
		}
		if &record.ACL.Entries[0] == &proc.ACL.Entries[0] {
			t.Fatal("provider capture borrowed current ACL backing")
		}
		count++
	}
	if count != 8 || !slices.Equal(globalRecord.ACL.Entries, globalEntries) || !slices.Equal(scopedRecord.ACL.Entries, scopedEntries) {
		t.Fatal("provider inventory/default ACL backing changed", count)
	}
	run("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA ip_defaults FROM ip_default_reader")
	run("ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM ip_default_reader")
	if stage.DefaultRecordsUnchanged(e.CatalogRead().State()) {
		t.Fatal("changed creation default escaped candidate publication observation")
	}
	var dependencies []access.RoleDependency
	access.InitialRoleDependencies(e.CatalogRead(), reader.Ref, &dependencies)
	if len(dependencies) != 8 {
		t.Fatal("captured default grantee lost historical dependencies", dependencies)
	}
	for _, address := range ext.Members {
		if address.ClassID == ids.ProcedureClassID && e.CatalogRead().State().InitialPrivilege(address) != installed.State().InitialPrivilege(address) {
			t.Fatal("later current/default ACL changes rewrote history", address)
		}
	}
	if !slices.Equal(globalRecord.ACL.Entries, globalEntries) || !slices.Equal(scopedRecord.ACL.Entries, scopedEntries) {
		t.Fatal("later default replacement mutated retained creation records")
	}
}

func TestPGInitPrivsProviderDefaultEdges(t *testing.T) {
	t.Run("explicit_empty_creation_acl", pgInitPrivsProviderDefaultExplicitEmpty)
	t.Run("public_already_absent", func(t *testing.T) {
		e := engine.New()
		for _, sql := range []string{
			"CREATE ROLE ip_default_reader",
			"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ip_default_reader WITH GRANT OPTION",
			"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC",
			"CREATE EXTENSION amcheck",
		} {
			enginetest.Run(t, e, sql)
		}
		reader, _ := e.Roles().Lookup("ip_default_reader")
		view := e.CatalogRead()
		ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
		count := 0
		for _, address := range ext.Members {
			if address.ClassID != ids.ProcedureClassID {
				continue
			}
			proc := view.MustProcedure(address)
			want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: proc.Owner}, Privileges: authorization.ExecutePrivilege},
				{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
			}}
			record := view.State().InitialPrivilege(address)
			if record == nil || record.PrivType != 'e' || !reflect.DeepEqual(record.ACL, want) {
				t.Fatal("script event without a PUBLIC delta omitted full history", proc.Name(), record)
			}
			count++
		}
		if count != 8 {
			t.Fatal("wrong supported script event count", count)
		}
	})
	t.Run("canceled_creation_acl", func(t *testing.T) {
		e := engine.New()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, done, err := enginetest.Operation(e, ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		base := e.CatalogRead()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		host := &schemacmds.ExtensionHost{Engine: e, Op: op, Auth: auth}
		stage, err := schemacmds.StageOf(host.BeginStage(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}))
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		cancel()
		_, err = host.RoutineCreationACLWithoutPublic(auth.State.CurrentUser, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
		e.Mu.Unlock()
		defer e.DiscardCatalogStage(stage)
		if !errors.Is(err, context.Canceled) || stage.DescriptorCount() != 0 || stage.InitialPrivilegeCount() != 0 || e.CatalogRead().State() != base.State() {
			t.Fatal("canceled script ACL published or staged a partial member", err)
		}
	})
}

// The contender is a complete, validated amcheck installation. Advancing the
// real default policy after validation must reject publication, not merely
// report a changed helper observation.
func TestPGInitPrivsProviderDefaultPublication(t *testing.T) {
	for _, existing := range []bool{true, false} {
		name := "absent_becomes_present"
		if existing {
			name = "changed_existing"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.Run(t, e, "CREATE ROLE ip_default_reader")
			if existing {
				enginetest.Run(t, e, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ip_default_reader")
			}
			e.Mu.Lock()
			stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "amcheck"}})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			key := catalog.DefaultACLKey{Creator: op.RoleState().CurrentUser, Class: authorization.DefaultRoutines}
			record, observed := stage.DefaultObservation(key)
			if !observed || (record != nil) != existing {
				t.Fatal("installation did not observe the intended default predecessor", record, observed)
			}
			extAddress := stage.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"})
			ext := stage.State().MustExtension(extAddress)
			if len(ext.Members) != 8 || stage.InitialPrivilegeCount() != 8 {
				t.Fatal("race contender was not a complete installation", len(ext.Members), stage.InitialPrivilegeCount())
			}
			pending, err := e.ValidateCatalogStage(op.Context(), stage)
			if err != nil {
				t.Fatal(err)
			}
			enginetest.Run(t, e, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ip_default_reader WITH GRANT OPTION")
			before := e.CatalogRead()
			if before.State().DefaultACL(key) == record {
				t.Fatal("race fixture did not advance the observed default")
			}
			_, err = e.PublishCatalogOperation(op, nil, pending, core.PublicationToken{Epoch: stage.Base.Epoch()})
			var conflict catalog.Conflict
			if !errors.As(err, &conflict) {
				t.Fatal("stale installation publication did not conflict", err)
			}
			after := e.CatalogRead()
			if after.State() != before.State() || after.Get(extAddress) != nil || after.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}) != (ids.ObjectAddress{}) {
				t.Fatal("rejected installation published an extension or changed the catalog")
			}
			for _, address := range ext.Members {
				proc := stage.State().MustProcedure(address)
				if !proc.ACL.Explicit || stage.State().InitialPrivilege(address) == nil {
					t.Fatal("contender lacked current/history ACL effects", address)
				}
				if after.Get(address) != nil || after.State().InitialPrivilege(address) != nil || after.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: proc.Namespace(), Name: proc.Name(), Signature: string(proc.Signature)}) != (ids.ObjectAddress{}) {
					t.Fatal("rejected installation published a member/current/history prefix", address)
				}
			}
			for address, history := range before.State().InitialPrivileges() {
				if after.State().InitialPrivilege(address) != history {
					t.Fatal("rejected installation changed existing history", address)
				}
			}
		})
	}
}

func pgInitPrivsProviderDefaultExplicitEmpty(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	owner := op.RoleState().CurrentUser
	key := catalog.DefaultACLKey{Creator: owner, Class: authorization.DefaultRoutines}
	// REL_18_6 acl.c aclmerge treats empty global + absent schema policy as
	// raw NULL. That is distinct from selectively revoking an empty object ACL.
	defaults := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(defaults)
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	empty := authorization.ACLValue{Explicit: true}
	defaults.ObserveDefault(key)
	defaults.SetDefaultACL(key, &catalog.DefaultACLRecord{OID: oid, ACL: empty})
	extensionUnitPublish(t, e, op, defaults)
	policy := e.CatalogRead().State().DefaultACL(key)
	creation := catalog.CreationDefaultACL(e.CatalogRead(), owner, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, authorization.DefaultRoutines)
	if creation.Explicit || len(creation.Entries) != 0 {
		t.Fatal("empty global policy did not normalize to raw NULL", creation)
	}
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "amcheck"}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	extensionUnitPublish(t, e, op, stage)
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
	if len(ext.Members) != 8 {
		t.Fatal("explicit empty defaults changed installed membership", len(ext.Members))
	}
	for _, address := range ext.Members {
		proc := view.MustProcedure(address)
		want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
		}}
		history := view.State().InitialPrivilege(address)
		if !reflect.DeepEqual(proc.ACL, want) || history == nil || history.PrivType != 'e' || !reflect.DeepEqual(history.ACL, want) {
			t.Fatal("empty policy did not follow release NULL-merge creation semantics", address, proc.ACL, history)
		}
	}
	// The provider above does not create an explicit-empty object ACL from an
	// empty global policy. Exercise that separate boundary through its actual
	// selective revoke operation, using the candidate/default record as input,
	// then publish the result and historical deletion through the real stage.
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	emptyStage := e.BeginCatalogStage(auth.Catalog)
	emptyStage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(emptyStage)
	input := emptyStage.Base.State().DefaultACL(key)
	emptyStage.ObserveDefault(key)
	revoked, changed, err := authorization.ChangeObjectACL(op.Context(), auth.Roles, input.ACL, owner, owner,
		[]authorization.ACLGrantee{{Public: true}}, authorization.ExecutePrivilege, false, true, false, false)
	if err != nil || changed || !revoked.Explicit || len(revoked.Entries) != 0 {
		t.Fatal("PUBLIC revoke synthesized baseline from explicit-empty ACL", revoked, changed, err)
	}
	for _, address := range ext.Members {
		proc := *view.MustProcedure(address)
		proc.ACL = revoked
		e.Mu.Lock()
		proc.ObjectRevision, err = e.Catalog.RevisionLocked()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err := emptyStage.Replace(&proc); err != nil {
			t.Fatal(err)
		}
		emptyStage.RecordInitialPrivilege(address, 'e', revoked)
	}
	extensionUnitPublish(t, e, op, emptyStage)
	published := e.CatalogRead()
	for _, address := range ext.Members {
		acl := published.MustProcedure(address).ACL
		if !acl.Explicit || len(acl.Entries) != 0 || published.State().InitialPrivilege(address) != nil {
			t.Fatal("explicit-empty script event synthesized baseline or phantom history", address, acl)
		}
		if view.State().InitialPrivilege(address) == nil {
			t.Fatal("empty publication mutated retained historical root", address)
		}
	}
	if published.State().DefaultACL(key) != policy || !policy.ACL.Explicit || len(policy.ACL.Entries) != 0 {
		t.Fatal("selective script revoke changed explicit empty policy")
	}
}

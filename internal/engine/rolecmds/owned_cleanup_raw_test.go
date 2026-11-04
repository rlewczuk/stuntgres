package rolecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestOwnedCleanupRawPrefix(t *testing.T) {
	e, owner, source, _, reader := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE TABLE cleanup_raw(id serial PRIMARY KEY,v integer)")
	ownershipUnitExec(t, e, "CREATE TABLE cleanup_survivor(v integer)")
	ownershipUnitExec(t, e, "GRANT SET ON PARAMETER work_mem TO ownership_source")
	ownershipUnitExec(t, e, "CREATE POLICY cleanup_raw_policy ON cleanup_raw TO ownership_source USING (true)")
	base := e.CatalogRead()
	find := func(name string) *catalog.Relation {
		return base.MustRelation(base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name}))
	}
	rawTable, survivor, seq := find("cleanup_raw"), find("cleanup_survivor"), find("cleanup_raw_id_seq")
	index := find("cleanup_raw_pkey")
	parameter := base.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: "work_mem"})
	if parameter == (ids.ObjectAddress{}) {
		t.Fatal("missing genuine parameter ACL descriptor")
	}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		objectACLUnitEntry(owner, source, authorization.SelectPrivilege, 0),
		objectACLUnitEntry(owner, reader, authorization.SelectPrivilege, 0),
	}}
	setup := e.BeginCatalogStage(base)
	for _, address := range []ids.ObjectAddress{rawTable.Address(), {ClassID: ids.RelationClassID, ObjectID: rawTable.Address().ObjectID, SubID: rawTable.Attributes[1]}, seq.Address(), survivor.Address()} {
		setup.RecordInitialPrivilege(address, 'i', acl)
	}
	ownershipUnitPublish(t, e, setup)
	base = e.CatalogRead()
	retained := base.State()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.RoleRevision = e.RoleStore.Published.Revision
	// Genuine earlier private history is the initial input, not Base alone.
	privateACL := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.SelectPrivilege, 0)}}
	stage.RecordInitialPrivilege(survivor.Address(), 'i', privateACL)
	e.Mu.Lock()
	err := stage.SelectDropRoot(stage.BaseRef(rawTable.Address()), false)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	drop := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	raw, err := drop.PrepareSelectedRawDrops()
	if err != nil {
		t.Fatal(err)
	}
	doomed := map[ids.ObjectAddress]bool{}
	for _, effect := range raw.Effects() {
		if effect.Remove.Address != (ids.ObjectAddress{}) {
			doomed[effect.Remove.Address] = true
		}
	}
	if !doomed[rawTable.Address()] || !doomed[index.Address()] || !doomed[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rawTable.RowType)}] || doomed[seq.Address()] {
		t.Fatal("raw prefix did not match actual raw worker/indexEvolution, or included later owned tail", doomed)
	}
	cleanup, err := role.PrepareOwnedCleanup(context.Background(), stage, e.RoleStore.PublishedRead(&e.Catalog.Owner, base), e.RoleStore.Published, map[authorization.PrincipalRef]bool{source: true}, raw)
	if err != nil {
		t.Fatal(err)
	}
	selectedPolicy := ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: rawTable.Security.Policies[0].OID}
	foundPolicy := false
	for _, root := range cleanup.DeletionRoots() {
		foundPolicy = foundPolicy || root == selectedPolicy
	}
	if !foundPolicy || !cleanup.AddsDeletionRoot() {
		t.Fatal("selected policy on raw-doomed relation lost its genuine collected deletion root")
	}
	if stage.Removed(rawTable.Address()) || stage.RoleCandidate() != nil || stage.StageRead().Get(parameter) == nil {
		t.Fatal("preparation executed physical effects")
	}
	dependencies, err := drop.PrepareExpressionDependencies([]ids.ObjectAddress{rawTable.Address()}, parser.DropCascade, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.ApplySelectedDropRoots(dependencies); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.EvolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = cleanup.Apply(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	current := stage.StageRead()
	if current.Get(seq.Address()) == nil {
		t.Fatal("later owned sequence retired during raw/cleanup prefix")
	}
	record := current.State().InitialPrivilege(seq.Address())
	if record == nil || record.PrivType != 'i' || len(record.ACL.Entries) != 1 || record.ACL.Entries[0].Grantee.Role != reader {
		t.Fatal("live secondary sequence initial history was skipped or owner/origin changed", record)
	}
	if current.State().InitialPrivilege(survivor.Address()) != nil {
		t.Fatal("real private source should produce an explicit-empty tombstone, not resurrect Base")
	}
	if current.Get(parameter) != nil || current.State().InitialPrivilege(parameter) != nil {
		t.Fatal("cleanup parameter removal resurrected initial privileges")
	}
	for address := range current.State().InitialPrivileges() {
		if catalog.InitialPrivilegeRemoved(address, rawTable.Address()) {
			t.Fatal("raw whole/subID initial history resurrected", address)
		}
	}
	if retained.InitialPrivilege(rawTable.Address()) == nil || retained.InitialPrivilege(seq.Address()) == nil || retained.InitialPrivilege(survivor.Address()) == nil {
		t.Fatal("cleanup mutated retained immutable history")
	}
	if err = cleanup.Apply(context.Background(), stage); err != ids.ErrCatalogReplaced {
		t.Fatal("duplicate cleanup accepted", err)
	}
	if err = drop.ApplyExpressionDependencies(dependencies); err != nil {
		t.Fatal(err)
	}
	if err = drop.ApplyOwnedExpressionDependencies(dependencies); err != nil {
		t.Fatal(err)
	}
	if stage.StageRead().State().InitialPrivilege(seq.Address()) != nil || stage.StageRead().Get(seq.Address()) != nil {
		t.Fatal("captured owned physical tail failed")
	}
	t.Run("namespace_default_tombstone", ownedCleanupRawNamespace)
	t.Run("canonical_stable_subid", ownedCleanupRawSubID)
	t.Run("surviving_owner_invariant", ownedCleanupRealOwner)
}

func ownedCleanupRawNamespace(t *testing.T) {
	e, _, source, _, _ := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE SCHEMA cleanup_namespace")
	ownershipUnitExec(t, e, "ALTER DEFAULT PRIVILEGES FOR ROLE ownership_source IN SCHEMA cleanup_namespace GRANT SELECT ON TABLES TO ownership_reader")
	base := e.CatalogRead()
	namespace := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "cleanup_namespace"})
	key := catalog.DefaultACLKey{Creator: source, Class: authorization.DefaultTables, Schema: namespace}
	if base.State().DefaultACL(key) == nil {
		t.Fatal("missing genuine namespace default policy")
	}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.RoleRevision = e.RoleStore.Published.Revision
	e.Mu.Lock()
	err := stage.SelectDropRoot(stage.BaseRef(namespace), true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	drop := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	raw, err := drop.PrepareSelectedRawDrops()
	if err != nil {
		t.Fatal(err)
	}
	automatic := false
	for _, effect := range raw.Effects() {
		automatic = automatic || effect.SchemaDefaults == namespace
	}
	if !automatic {
		t.Fatal("canonical namespace default-ACL raw effect missing")
	}
	cleanup, err := role.PrepareOwnedCleanup(context.Background(), stage, e.RoleStore.PublishedRead(&e.Catalog.Owner, base), e.RoleStore.Published, map[authorization.PrincipalRef]bool{source: true}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cleanup.AddsDeletionRoot() {
		t.Fatal("raw-tombstoned creator policy no longer counted as collected root")
	}
	dependencies, err := drop.PrepareExpressionDependencies([]ids.ObjectAddress{namespace}, parser.DropCascade, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.ApplySelectedDropRoots(dependencies); err != nil {
		t.Fatal(err)
	}
	if err = cleanup.Apply(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	if stage.StageRead().State().DefaultACL(key) != nil || base.State().DefaultACL(key) == nil {
		t.Fatal("namespace tombstone resurrected or changed old read")
	}
}

func ownedCleanupRawSubID(t *testing.T) {
	e, owner, source, _, reader := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE TABLE cleanup_subid(a integer,b integer,c integer)")
	base := e.CatalogRead()
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "cleanup_subid"})
	old := base.MustRelation(address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.SelectPrivilege, 0), objectACLUnitEntry(owner, reader, authorization.SelectPrivilege, 0)}}
	setup := e.BeginCatalogStage(base)
	for _, sub := range old.Attributes {
		column := address
		column.SubID = sub
		setup.RecordInitialPrivilege(column, 'i', acl)
	}
	ownershipUnitPublish(t, e, setup)
	base = e.CatalogRead()
	old = base.MustRelation(address)
	ref := e.Catalog.Domain.Ref(base.Epoch(), address)
	raw := &table.DropPreparation{Base: base, Discovery: base, Old: old, Ref: ref}
	provider := &core.DropProvider{Base: base, Owner: e, Prepared: raw, Ctx: context.Background()}
	next, err := table.OrdinaryColumnDrop(provider, old, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "b"}, Mode: parser.DropRestrict})
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.RoleRevision = e.RoleStore.Published.Revision
	cleanup, err := role.PrepareOwnedCleanup(context.Background(), stage, e.RoleStore.PublishedRead(&e.Catalog.Owner, base), e.RoleStore.Published, map[authorization.PrincipalRef]bool{source: true}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	dropped := address
	dropped.SubID = old.Attributes[1]
	if stage.StageRead().State().InitialPrivilege(dropped) != nil {
		t.Fatal("canonical relation replacement did not retire dropped stable-subID history")
	}
	if err = cleanup.Apply(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	current := stage.StageRead()
	for _, sub := range old.Attributes {
		column := address
		column.SubID = sub
		record := current.State().InitialPrivilege(column)
		if original := base.State().InitialPrivilege(column); original == nil || original.PrivType != 'i' || !authorization.EqualACL(original.ACL, acl) {
			t.Fatal("cleanup changed retained stable-subID history", sub, original)
		}
		if sub == old.Attributes[1] {
			if record != nil {
				t.Fatal("dropped stable-subID initial history resurrected")
			}
			continue
		}
		expected := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, reader, authorization.SelectPrivilege, 0)}}
		if record == nil || record.PrivType != 'i' || !authorization.EqualACL(record.ACL, expected) || current.MustRelation(address).Owner != owner {
			t.Fatal("surviving stable-subID cleanup/origin/owner changed", sub, record)
		}
	}
}

func ownedCleanupRealOwner(t *testing.T) {
	e, owner, source, target, _ := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE TABLE cleanup_real_owner(v integer)")
	base := e.CatalogRead()
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "cleanup_real_owner"})
	setup := e.BeginCatalogStage(base)
	setup.RecordInitialPrivilege(address, 'i', authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.SelectPrivilege, 0)}})
	ownershipUnitPublish(t, e, setup)
	base = e.CatalogRead()
	original := base.State().InitialPrivilege(address)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	cleanup, err := role.PrepareOwnedCleanup(context.Background(), stage, e.RoleStore.PublishedRead(&e.Catalog.Owner, base), e.RoleStore.Published, map[authorization.PrincipalRef]bool{source: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := *base.MustRelation(address)
	next.Owner = target
	next.ObjectRevision++
	if err = stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	if err = cleanup.Apply(context.Background(), stage); err != ids.ErrCatalogReplaced {
		t.Fatal("cleanup substituted a convenient owner instead of validating real owner", err)
	}
	if e.CatalogRead().State().InitialPrivilege(address) != original || e.CatalogRead().MustRelation(address).Owner != owner {
		t.Fatal("failed owner invariant changed committed object/history")
	}
}

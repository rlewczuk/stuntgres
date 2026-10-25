package engine

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestDefaultPrivilegesCloneStage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "tables", "select", nil)
	base := f.e.CatalogRead()
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	record := base.State().DefaultACL(key)
	if record == nil || record.OID == 0 {
		t.Fatal("default row lacks producer-owned identity")
	}
	next := catalog.CloneState(base.State())
	next.OverlayDefaultACLs(next, map[catalog.DefaultACLKey]*catalog.DefaultACLRecord{key: nil})
	if next.DefaultACL(key) != nil || base.State().DefaultACL(key) != record {
		t.Fatal("clone overlay mutated source")
	}
	stage := f.e.BeginCatalogStage(base)
	stage.ObserveDefault(key)
	stage.SetDefaultACL(key, nil)
	staged := f.e.StageRead(stage)
	if staged.State().DefaultACL(key) != nil || base.State().DefaultACL(key) != record {
		t.Fatal("default-only stage lost tombstone")
	}
	changes := session.NewJournal()
	f.e.Mu.Lock()
	changes.RecordCandidateDefaults(f.e.Catalog.State, &stage.Candidate)
	private := f.e.PrivateCatalogLocked(changes)
	f.e.Mu.Unlock()
	if private.State().DefaultACL(key) != nil || changes.DefaultOriginals[key] != record {
		t.Fatal("default-only private overlay")
	}
	cloned := changes.Clone()
	delete(cloned.DefaultACLs, key)
	delete(cloned.DefaultOriginals, key)
	delete(cloned.DefaultReads, key)
	if _, ok := changes.DefaultACLs[key]; !ok || changes.DefaultOriginals[key] != record || changes.DefaultReads[key] != record {
		t.Fatal("cloned sparse maps aliased source")
	}
	// A cached descriptor-empty overlay must invalidate when defaults change.
	f.e.Mu.Lock()
	defaultACLChangeLocked(f.e, changes, key, record)
	fresh := f.e.PrivateCatalogLocked(changes)
	f.e.Mu.Unlock()
	if fresh.State().DefaultACL(key) != record || private.State().DefaultACL(key) != nil {
		t.Fatal("cached default-only read not refreshed immutably")
	}
}

func TestDefaultPrivilegesRetainedMessage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	committedCatalog, committedRoles := f.e.Catalog.State, f.e.RoleStore.Published
	scope, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.RetainMessage(); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	message := &batchUnitMessage{scope: scope, op: f.op}
	defer func() { _ = message.scope.FinishMessage(context.Canceled) }()
	if _, err = message.step(defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})); err != nil {
		t.Fatal(err)
	}
	changes := f.op.Session.Transactions.Catalog
	f.e.Mu.Lock()
	private := f.e.PrivateCatalogLocked(changes)
	f.e.Mu.Unlock()
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	if f.e.Catalog.State != committedCatalog || f.e.RoleStore.Published != committedRoles || private.State().DefaultACL(key) == nil || changes.Roles == nil || changes.RolesBase != committedRoles {
		t.Fatal("retained utility leaked or lost private state")
	}
	stage := f.e.BeginCatalogStage(private)
	table := defaultUnitTable(t, f, stage, "retained", f.owner)
	f.e.Mu.Lock()
	EngineStageCatalogChangesLocked(f.e, stage, changes)
	combined := f.e.PrivateCatalogLocked(changes)
	err = f.e.CatalogPreflightLocked(changes)
	f.e.Mu.Unlock()
	if err != nil || combined.Get(table.Address()) != table || authorization.ACLRights(table.Security.ACL, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege, false)&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("later retained creation missed default", err)
	}
	if _, observed := changes.DefaultReads[key]; observed {
		t.Fatal("private default output was fenced as committed input")
	}
	prepared, err := EnginePreparePrivateCatalog(f.e, context.Background(), changes, committedCatalog.Epoch())
	if err != nil || prepared.DefaultACL(key) == nil || prepared.DefaultACL(key).OID != private.State().DefaultACL(key).OID || prepared.Get(table.Address()) != table {
		t.Fatal("combined preparation lost default or object", err)
	}
	late := defaultUnitStatement("sequences", "usage", []string{"default_owner", "missing"}, nil, []string{"default_reader"})
	_, err = message.step(late)
	defaultUnitDiagnostic(t, err, "42704", `role "missing" does not exist`)
	// Reject through the actual owning message admission; detached reads retain
	// their earlier private result without installing either committed root.
	defaultUnitDiagnostic(t, message.scope.FinishMessage(err), "42704", `role "missing" does not exist`)
	if f.op.Session.Transactions.Catalog != nil || f.op.Session.Transactions.RetainedMessage || f.e.Catalog.State != committedCatalog || f.e.RoleStore.Published != committedRoles || combined.Get(table.Address()) != table || private.State().DefaultACL(key) == nil {
		t.Fatal("retained failure changed committed or retained snapshots")
	}
}

func TestTransactionalPrivilegesCreation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	committed, roles := f.e.Catalog.State, f.e.RoleStore.Published
	if _, err := f.e.Begin(f.op, session.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "tables", "select", nil)
	changes := f.op.Session.Transactions.Catalog
	if changes == nil || changes.Roles == nil || changes.RolesBase != roles || f.e.Catalog.State != committed || f.e.RoleStore.Published != roles {
		t.Fatal("private default directive leaked or lost its paired roles")
	}
	f.e.Mu.Lock()
	private := f.e.PrivateCatalogLocked(changes)
	f.e.Mu.Unlock()
	stage := f.e.BeginCatalogStage(private)
	own := defaultUnitTable(t, f, stage, "private_creator", f.owner)
	other := defaultUnitTable(t, f, stage, "different_creator", f.member)
	right := func(table *catalog.Relation) authorization.Privileges {
		return authorization.ACLRights(table.Security.ACL, table.Owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege, false)
	}
	if right(own) == 0 || right(other) != 0 {
		t.Fatal("private actual-creator policy not materialized")
	}
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	merged := changes.Clone()
	f.e.Mu.Lock()
	EngineStageCatalogChangesLocked(f.e, stage, merged)
	f.e.Mu.Unlock()
	if _, observed := merged.DefaultReads[key]; observed {
		t.Fatal("private policy recorded as a committed journal predecessor")
	}
	saved := own.Security.ACL
	revoke := defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
	revoke.Revoke = true
	if err := f.apply(revoke); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, own.Security.ACL) || f.e.Catalog.State != committed || f.e.RoleStore.Published != roles {
		t.Fatal("later private directive rewrote an earlier object or committed root")
	}
}

func TestDefaultPrivilegesDependencies(t *testing.T) {
	f := newDefaultUnitFixture(t)
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		f.grant(t, class, "all", nil)
	}
	var got []access.RoleDependency
	access.DefaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, &got)
	descriptions := make([]string, len(got))
	for i, dep := range got {
		descriptions[i] = dep.Description
		if dep.Kind != 'a' {
			t.Fatal("recipient dependency kind", dep)
		}
	}
	sort.Strings(descriptions)
	want := []string{"privileges for default privileges on new functions belonging to role default_owner", "privileges for default privileges on new large objects belonging to role default_owner", "privileges for default privileges on new relations belonging to role default_owner", "privileges for default privileges on new schemas belonging to role default_owner", "privileges for default privileges on new sequences belonging to role default_owner", "privileges for default privileges on new types belonging to role default_owner"}
	sort.Strings(want)
	if !reflect.DeepEqual(descriptions, want) {
		t.Fatal(descriptions, want)
	}
	got = nil
	access.DefaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.owner, &got)
	if len(got) != 6 {
		t.Fatal("creator default dependencies", got)
	}
	for _, dep := range got {
		if dep.Kind != 'o' {
			t.Fatal("creator not owner dependency", dep)
		}
	}
	removed := cloneRoleFixture(f.e.RoleStore.Published)
	retireFixtureRole(removed, f.reader)
	changes := session.NewJournal()
	changes.Roles = removed
	changes.RetiredRoles = map[authorization.PrincipalRef]struct{}{f.reader: {}}
	if err := EngineRoleRetirementPreflightLocked(f.e, changes, f.e.Catalog.State); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("late default recipient retirement", err)
	}
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	table := defaultUnitTable(t, f, stage, "materialized", f.owner)
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		s := defaultUnitStatement(class, "all", []string{"default_owner"}, nil, []string{"default_reader"})
		s.Revoke = true
		if err := f.apply(s); err != nil {
			t.Fatal(err)
		}
	}
	if f.e.Catalog.State.DefaultACLCount() != 0 || access.DefaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, nil) {
		t.Fatal("baseline reversal retained default dependencies")
	}
	if !access.DescriptorRoleDependencies(table, f.reader, nil) {
		t.Fatal("default reversal erased materialized dependency")
	}
	if err := EngineRoleRetirementPreflightLocked(f.e, changes, f.e.Catalog.State); err != nil {
		t.Fatal("reversed default still blocked retirement", err)
	}
	candidate := catalog.CloneState(f.e.Catalog.State)
	candidate.SetDescriptor(table.Address(), table)
	if err := EngineRoleRetirementPreflightLocked(f.e, changes, candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("materialized dependency omitted from final retirement", err)
	}
	empty := defaultUnitStatement("large objects", "all", []string{"default_owner"}, nil, []string{"default_owner"})
	empty.Revoke = true
	if err := f.apply(empty); err != nil {
		t.Fatal(err)
	}
	if !access.DefaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.owner, nil) {
		t.Fatal("stored empty lost creator dependency")
	}
}

func TestDefaultPrivilegesSchemaLifecycle(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "tables", "select", nil)
	f.grant(t, "tables", "insert", []string{"default_schema"})
	global := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	scoped := catalog.DefaultACLKey{Creator: f.owner, Schema: f.schema, Class: authorization.DefaultTables}
	old := f.e.CatalogRead()
	record := old.State().DefaultACL(scoped)
	stage := f.e.BeginCatalogStage(old)
	namespace := *old.MustNamespace(f.schema)
	namespace.ObjectName = "renamed"
	namespace.ObjectRevision++
	if err := stage.Replace(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACL(scoped) != record {
		t.Fatal("rename lost identity-bound scoped policy")
	}
	var dependencies []access.RoleDependency
	access.DefaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, &dependencies)
	found := false
	for _, dep := range dependencies {
		if dep.Description == "privileges for default privileges on new relations belonging to role default_owner in schema renamed" {
			found = true
		}
	}
	if !found {
		t.Fatal("dependency description ignored renamed namespace", dependencies)
	}
	before := f.e.CatalogRead()
	drop := f.e.BeginCatalogStage(before)
	ref := f.e.Catalog.Domain.Ref(before.Epoch(), f.schema)
	if err := StageRemoveObject(drop, ref, true); err != nil {
		t.Fatal(err)
	}
	delta, changed := drop.DefaultACLChange(scoped)
	observed, readInput := drop.DefaultObservation(scoped)
	if !changed || delta != nil || !readInput || observed != record {
		t.Fatal("schema cleanup not staged")
	}
	if err := defaultUnitPublish(t, f.e, drop); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACL(scoped) != nil || f.e.Catalog.State.DefaultACL(global) == nil || old.State().DefaultACL(scoped) != record {
		t.Fatal("schema drop/global/source semantics")
	}
	fresh := f.e.BeginCatalogStage(f.e.CatalogRead())
	namespace.ObjectAddress.ObjectID = 20011
	namespace.ObjectName = "default_schema"
	namespace.ObjectRevision++
	if err := fresh.Create(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := defaultUnitPublish(t, f.e, fresh); err != nil {
		t.Fatal(err)
	}
	acl := catalog.CreationDefaultACL(f.e.CatalogRead(), f.owner, namespace.Address(), authorization.DefaultTables)
	rights := authorization.ACLRights(acl, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege|authorization.InsertPrivilege, false)
	if rights&authorization.InsertPrivilege != 0 || rights&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("recreated namespace resurrected scoped defaults", acl)
	}
	// A concurrent newly-created scoped key must also be fenced when the staged
	// removal observed no such key; otherwise namespace cleanup could orphan it.
	stale := f.e.BeginCatalogStage(f.e.CatalogRead())
	ref.Address = namespace.Address()
	ref.Epoch = stale.Base.Epoch()
	if err := StageRemoveObject(stale, ref, true); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "sequences", "usage", []string{"default_schema"})
	if err := defaultUnitPublish(t, f.e, stale); err != (catalog.Conflict{}) {
		t.Fatal("new scoped key escaped schema retirement fence", err)
	}
}

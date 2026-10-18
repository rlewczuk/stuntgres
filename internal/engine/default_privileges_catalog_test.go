package engine

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestDefaultPrivilegesCloneStage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "tables", "select", nil)
	base := f.e.CatalogRead()
	key := defaultACLKey{creator: f.owner, class: authorization.DefaultTables}
	record := base.state.defaultACLs[key]
	next := cloneCatalog(base.state)
	next.defaultACLs = overlayDefaultACLs(next.defaultACLs, map[defaultACLKey]*defaultACLRecord{key: nil})
	if next.defaultACLs[key] != nil || base.state.defaultACLs[key] != record {
		t.Fatal("clone overlay mutated source")
	}
	stage := f.e.BeginCatalogStage(base)
	stage.defaultACLs = map[defaultACLKey]*defaultACLRecord{key: nil}
	stage.defaultReads = map[defaultACLKey]*defaultACLRecord{key: record}
	staged := f.e.stageRead(stage)
	if staged.state.defaultACLs[key] != nil || base.state.defaultACLs[key] != record {
		t.Fatal("default-only stage lost tombstone")
	}
	changes := newCatalogChanges()
	f.e.mu.Lock()
	f.e.stageDefaultChangesLocked(stage, changes)
	private := f.e.privateCatalogLocked(changes)
	f.e.mu.Unlock()
	if private.state.defaultACLs[key] != nil || changes.defaultOriginals[key] != record {
		t.Fatal("default-only private overlay")
	}
	cloned := cloneCatalogChanges(changes)
	delete(cloned.defaultACLs, key)
	delete(cloned.defaultOriginals, key)
	delete(cloned.defaultReads, key)
	if _, ok := changes.defaultACLs[key]; !ok || changes.defaultOriginals[key] != record || changes.defaultReads[key] != record {
		t.Fatal("cloned sparse maps aliased source")
	}
	// A cached descriptor-empty overlay must invalidate when defaults change.
	f.e.mu.Lock()
	f.e.defaultACLChangeLocked(changes, key, record)
	fresh := f.e.privateCatalogLocked(changes)
	f.e.mu.Unlock()
	if fresh.state.defaultACLs[key] != record || private.state.defaultACLs[key] != nil {
		t.Fatal("cached default-only read not refreshed immutably")
	}
}

func TestDefaultPrivilegesRetainedMessage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	committedCatalog, committedRoles := f.e.catalog.state, f.e.roles
	f.op.session.transactions.retainedMessage = true
	f.grant(t, "tables", "select", nil)
	changes := f.op.session.transactions.catalog
	f.e.mu.Lock()
	private := f.e.privateCatalogLocked(changes)
	f.e.mu.Unlock()
	key := defaultACLKey{creator: f.owner, class: authorization.DefaultTables}
	if f.e.catalog.state != committedCatalog || f.e.roles != committedRoles || private.state.defaultACLs[key] == nil || changes.roles == nil || changes.rolesBase != committedRoles {
		t.Fatal("retained utility leaked or lost private state")
	}
	stage := f.e.BeginCatalogStage(private)
	table := defaultUnitTable(t, f, stage, "retained", f.owner)
	f.e.mu.Lock()
	f.e.stageCatalogChangesLocked(stage, changes)
	combined := f.e.privateCatalogLocked(changes)
	err := f.e.catalogPreflightLocked(changes)
	f.e.mu.Unlock()
	if err != nil || combined.Get(table.address) != table || authorization.ACLRights(table.acl, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege, false)&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("later retained creation missed default", err)
	}
	if _, observed := changes.defaultReads[key]; observed {
		t.Fatal("private default output was fenced as committed input")
	}
	prepared, err := f.e.preparePrivateCatalog(context.Background(), changes, committedCatalog.epoch)
	if err != nil || prepared.defaultACLs[key] == nil || prepared.Get(table.address) != table {
		t.Fatal("combined preparation lost default or object", err)
	}
	late := defaultUnitStatement("sequences", "usage", []string{"default_owner", "missing"}, nil, []string{"default_reader"})
	defaultUnitDiagnostic(t, f.apply(late), "42704", `role "missing" does not exist`)
	// The real retained-message rejection path discards the same entire overlay;
	// independently prove that neither root was ever mutated and detached reads
	// retain the earlier private result after the owning overlay is discarded.
	f.op.session.transactions.catalog = nil
	f.op.session.transactions.retainedMessage = false
	if f.e.catalog.state != committedCatalog || f.e.roles != committedRoles || combined.Get(table.address) != table || private.state.defaultACLs[key] == nil {
		t.Fatal("retained failure changed committed or retained snapshots")
	}
}

func TestDefaultPrivilegesDependencies(t *testing.T) {
	f := newDefaultUnitFixture(t)
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		f.grant(t, class, "all", nil)
	}
	var got []roleDependency
	defaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, &got)
	descriptions := make([]string, len(got))
	for i, dep := range got {
		descriptions[i] = dep.description
		if dep.kind != 'a' {
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
	defaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.owner, &got)
	if len(got) != 6 {
		t.Fatal("creator default dependencies", got)
	}
	for _, dep := range got {
		if dep.kind != 'o' {
			t.Fatal("creator not owner dependency", dep)
		}
	}
	removed := cloneRoleFixture(f.e.roles)
	retireFixtureRole(removed, f.reader)
	changes := newCatalogChanges()
	changes.roles = removed
	changes.retiredRoles = map[authorization.PrincipalRef]struct{}{f.reader: {}}
	if err := f.e.roleRetirementPreflightLocked(changes, f.e.catalog.state); !errors.Is(err, ids.ErrCatalogReplaced) {
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
	if len(f.e.catalog.state.defaultACLs) != 0 || defaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, nil) {
		t.Fatal("baseline reversal retained default dependencies")
	}
	if !descriptorRoleDependencies(table, f.reader, nil) {
		t.Fatal("default reversal erased materialized dependency")
	}
	if err := f.e.roleRetirementPreflightLocked(changes, f.e.catalog.state); err != nil {
		t.Fatal("reversed default still blocked retirement", err)
	}
	candidate := cloneCatalog(f.e.catalog.state)
	candidate.setDescriptor(table.address, table)
	if err := f.e.roleRetirementPreflightLocked(changes, candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("materialized dependency omitted from final retirement", err)
	}
	empty := defaultUnitStatement("large objects", "all", []string{"default_owner"}, nil, []string{"default_owner"})
	empty.Revoke = true
	if err := f.apply(empty); err != nil {
		t.Fatal(err)
	}
	if !defaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.owner, nil) {
		t.Fatal("stored empty lost creator dependency")
	}
}

func TestDefaultPrivilegesSchemaLifecycle(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "tables", "select", nil)
	f.grant(t, "tables", "insert", []string{"default_schema"})
	global := defaultACLKey{creator: f.owner, class: authorization.DefaultTables}
	scoped := defaultACLKey{creator: f.owner, schema: f.schema, class: authorization.DefaultTables}
	old := f.e.CatalogRead()
	record := old.state.defaultACLs[scoped]
	stage := f.e.BeginCatalogStage(old)
	namespace := *old.MustNamespace(f.schema)
	namespace.name = "renamed"
	namespace.revision++
	if err := stage.Replace(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	if f.e.catalog.state.defaultACLs[scoped] != record {
		t.Fatal("rename lost identity-bound scoped policy")
	}
	var dependencies []roleDependency
	defaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, &dependencies)
	found := false
	for _, dep := range dependencies {
		if dep.description == "privileges for default privileges on new relations belonging to role default_owner in schema renamed" {
			found = true
		}
	}
	if !found {
		t.Fatal("dependency description ignored renamed namespace", dependencies)
	}
	before := f.e.CatalogRead()
	drop := f.e.BeginCatalogStage(before)
	ref := f.e.catalog.domain.Ref(before.Epoch(), f.schema)
	if err := drop.removeObject(ref, true); err != nil {
		t.Fatal(err)
	}
	if drop.defaultACLs[scoped] != nil || drop.defaultReads[scoped] != record {
		t.Fatal("schema cleanup not staged")
	}
	if err := defaultUnitPublish(t, f.e, drop); err != nil {
		t.Fatal(err)
	}
	if f.e.catalog.state.defaultACLs[scoped] != nil || f.e.catalog.state.defaultACLs[global] == nil || old.state.defaultACLs[scoped] != record {
		t.Fatal("schema drop/global/source semantics")
	}
	fresh := f.e.BeginCatalogStage(f.e.CatalogRead())
	namespace.address.ObjectID = 20011
	namespace.name = "default_schema"
	namespace.revision++
	if err := fresh.Create(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := defaultUnitPublish(t, f.e, fresh); err != nil {
		t.Fatal(err)
	}
	acl := creationDefaultACL(f.e.CatalogRead(), f.owner, namespace.address, authorization.DefaultTables)
	rights := authorization.ACLRights(acl, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege|authorization.InsertPrivilege, false)
	if rights&authorization.InsertPrivilege != 0 || rights&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("recreated namespace resurrected scoped defaults", acl)
	}
	// A concurrent newly-created scoped key must also be fenced when the staged
	// removal observed no such key; otherwise namespace cleanup could orphan it.
	stale := f.e.BeginCatalogStage(f.e.CatalogRead())
	ref.Address = namespace.address
	ref.Epoch = stale.base.Epoch()
	if err := stale.removeObject(ref, true); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "sequences", "usage", []string{"default_schema"})
	if err := defaultUnitPublish(t, f.e, stale); err != (CatalogConflict{}) {
		t.Fatal("new scoped key escaped schema retirement fence", err)
	}
}

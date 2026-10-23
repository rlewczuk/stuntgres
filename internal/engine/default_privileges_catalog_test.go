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
	staged := f.e.stageRead(stage)
	if staged.State().DefaultACL(key) != nil || base.State().DefaultACL(key) != record {
		t.Fatal("default-only stage lost tombstone")
	}
	changes := session.NewJournal()
	f.e.mu.Lock()
	changes.RecordCandidateDefaults(f.e.catalog.state, &stage.Candidate)
	private := f.e.privateCatalogLocked(changes)
	f.e.mu.Unlock()
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
	f.e.mu.Lock()
	f.e.defaultACLChangeLocked(changes, key, record)
	fresh := f.e.privateCatalogLocked(changes)
	f.e.mu.Unlock()
	if fresh.State().DefaultACL(key) != record || private.State().DefaultACL(key) != nil {
		t.Fatal("cached default-only read not refreshed immutably")
	}
}

func TestDefaultPrivilegesRetainedMessage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	committedCatalog, committedRoles := f.e.catalog.state, f.e.roles.published
	f.op.Session.Transactions.RetainedMessage = true
	f.grant(t, "tables", "select", nil)
	changes := f.op.Session.Transactions.Catalog
	f.e.mu.Lock()
	private := f.e.privateCatalogLocked(changes)
	f.e.mu.Unlock()
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	if f.e.catalog.state != committedCatalog || f.e.roles.published != committedRoles || private.State().DefaultACL(key) == nil || changes.Roles == nil || changes.RolesBase != committedRoles {
		t.Fatal("retained utility leaked or lost private state")
	}
	stage := f.e.BeginCatalogStage(private)
	table := defaultUnitTable(t, f, stage, "retained", f.owner)
	f.e.mu.Lock()
	f.e.stageCatalogChangesLocked(stage, changes)
	combined := f.e.privateCatalogLocked(changes)
	err := f.e.catalogPreflightLocked(changes)
	f.e.mu.Unlock()
	if err != nil || combined.Get(table.Address()) != table || authorization.ACLRights(table.Security.ACL, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege, false)&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("later retained creation missed default", err)
	}
	if _, observed := changes.DefaultReads[key]; observed {
		t.Fatal("private default output was fenced as committed input")
	}
	prepared, err := f.e.preparePrivateCatalog(context.Background(), changes, committedCatalog.Epoch())
	if err != nil || prepared.DefaultACL(key) == nil || prepared.DefaultACL(key).OID != private.State().DefaultACL(key).OID || prepared.Get(table.Address()) != table {
		t.Fatal("combined preparation lost default or object", err)
	}
	late := defaultUnitStatement("sequences", "usage", []string{"default_owner", "missing"}, nil, []string{"default_reader"})
	defaultUnitDiagnostic(t, f.apply(late), "42704", `role "missing" does not exist`)
	// The real retained-message rejection path discards the same entire overlay;
	// independently prove that neither root was ever mutated and detached reads
	// retain the earlier private result after the owning overlay is discarded.
	f.op.Session.Transactions.Catalog = nil
	f.op.Session.Transactions.RetainedMessage = false
	if f.e.catalog.state != committedCatalog || f.e.roles.published != committedRoles || combined.Get(table.Address()) != table || private.State().DefaultACL(key) == nil {
		t.Fatal("retained failure changed committed or retained snapshots")
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
	removed := cloneRoleFixture(f.e.roles.published)
	retireFixtureRole(removed, f.reader)
	changes := session.NewJournal()
	changes.Roles = removed
	changes.RetiredRoles = map[authorization.PrincipalRef]struct{}{f.reader: {}}
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
	if f.e.catalog.state.DefaultACLCount() != 0 || access.DefaultRoleDependencies(f.e.CatalogRead(), f.e.Roles(), f.reader, nil) {
		t.Fatal("baseline reversal retained default dependencies")
	}
	if !access.DescriptorRoleDependencies(table, f.reader, nil) {
		t.Fatal("default reversal erased materialized dependency")
	}
	if err := f.e.roleRetirementPreflightLocked(changes, f.e.catalog.state); err != nil {
		t.Fatal("reversed default still blocked retirement", err)
	}
	candidate := catalog.CloneState(f.e.catalog.state)
	candidate.SetDescriptor(table.Address(), table)
	if err := f.e.roleRetirementPreflightLocked(changes, candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
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
	if f.e.catalog.state.DefaultACL(scoped) != record {
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
	ref := f.e.catalog.Domain.Ref(before.Epoch(), f.schema)
	if err := drop.removeObject(ref, true); err != nil {
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
	if f.e.catalog.state.DefaultACL(scoped) != nil || f.e.catalog.state.DefaultACL(global) == nil || old.State().DefaultACL(scoped) != record {
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
	if err := stale.removeObject(ref, true); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "sequences", "usage", []string{"default_schema"})
	if err := defaultUnitPublish(t, f.e, stale); err != (catalog.Conflict{}) {
		t.Fatal("new scoped key escaped schema retirement fence", err)
	}
}

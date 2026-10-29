package core_test

import (
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestPGInitPrivsStorage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	base := f.e.CatalogRead()
	informationSchema := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "information_schema"})
	namespace, exists := base.Namespace(informationSchema)
	if !exists || !namespace.ACL.Explicit || base.State().InitialPrivilege(informationSchema) != nil {
		t.Fatal("post-snapshot information_schema grants became bootstrap history")
	}
	for object, descriptor := range base.Descriptors() {
		if descriptor.Namespace() == informationSchema && base.State().InitialPrivilege(object) != nil {
			t.Fatal("post-snapshot information_schema object has initial history", object)
		}
	}
	address := f.schema
	column := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: address.ObjectID, SubID: 2}
	whole := column
	whole.SubID = 0
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
		{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: authorization.UsagePrivilege, GrantOptions: authorization.UsagePrivilege},
	}}
	original := append([]authorization.ACLEntry(nil), acl.Entries...)
	candidate := catalog.NewCandidate(base)
	candidate.RecordInitialPrivilege(address, 'e', acl)
	candidate.RecordInitialPrivilege(column, 'e', acl)
	candidate.RecordInitialPrivilege(whole, 'e', acl)
	retained := candidate.State()
	first := retained.InitialPrivilege(address)
	if first == nil || first.PrivType != 'e' || &first.ACL.Entries[0] == &acl.Entries[0] {
		t.Fatal("snapshot does not own incoming ACL backing")
	}
	acl.Entries[0].Privileges = 0
	if !reflect.DeepEqual(first.ACL.Entries, original) || base.State().InitialPrivilege(address) != nil {
		t.Fatal("capture borrowed caller scratch or mutated its base")
	}
	candidate.RecordInitialPrivilege(address, 'i', authorization.ACLValue{Explicit: true, Entries: original[:1]})
	second := candidate.State()
	if second.InitialPrivilege(address).PrivType != 'e' || first == second.InitialPrivilege(address) || !reflect.DeepEqual(first.ACL.Entries, original) {
		t.Fatal("replacement changed origin or retained snapshot backing")
	}
	candidate.RemoveInitialPrivileges(whole)
	next := candidate.State()
	if next.InitialPrivilege(whole) != nil || next.InitialPrivilege(column) != nil || next.InitialPrivilege(address) != second.InitialPrivilege(address) {
		t.Fatal("whole-object deletion confused classes or retained a column")
	}
	if retained.InitialPrivilege(column) == nil || retained.InitialPrivilege(whole) == nil {
		t.Fatal("deletion mutated an exported state")
	}
	clone := catalog.CloneState(retained)
	if clone.InitialPrivilege(address) != first {
		t.Fatal("unchanged immutable records were not shared")
	}
	candidate.RecordInitialPrivilege(address, 'e', authorization.ACLValue{Explicit: true})
	if candidate.State().InitialPrivilege(address) != nil {
		t.Fatal("empty ACL was retained as a snapshot")
	}
	t.Run("column_retirement", pgInitPrivsColumnRetirement)
	t.Run("canonical_baseline", pgInitPrivsCanonicalBaseline)
}

func TestPGInitPrivsPublication(t *testing.T) {
	f := newDefaultUnitFixture(t)
	base := f.e.CatalogRead()
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: authorization.UsagePrivilege}}}
	stage := f.e.BeginCatalogStage(base)
	defer f.e.DiscardCatalogStage(stage)
	stage.RecordInitialPrivilege(f.schema, 'e', acl)
	frontier := f.e.Catalog.NextOID()
	if err := defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	published := f.e.Catalog.State
	if published.InitialPrivilege(f.schema) == nil || base.State().InitialPrivilege(f.schema) != nil || f.e.Catalog.NextOID() != frontier {
		t.Fatal("initial-only publication lost its delta or allocated an OID")
	}
	candidate := catalog.NewCandidate(base)
	candidate.RecordInitialPrivilege(f.schema, 'e', acl)
	if candidate.InitialPrivilegesUnchanged(published) {
		t.Fatal("observed absence failed to conflict with a new record")
	}
	candidate = catalog.NewCandidate(f.e.CatalogRead())
	candidate.RecordInitialPrivilege(f.schema, 'e', acl)
	if candidate.InitialPrivilegeCount() != 0 || candidate.State().InitialPrivilege(f.schema) != published.InitialPrivilege(f.schema) {
		t.Fatal("equal historical ACL allocated a redundant replacement")
	}
	changedACL := authorization.ACLValue{Explicit: true, Entries: append([]authorization.ACLEntry(nil), acl.Entries...)}
	changedACL.Entries[0].Privileges |= authorization.CreatePrivilege
	candidate.RecordInitialPrivilege(f.schema, 'e', changedACL)
	changedDescriptor := catalog.CloneState(published)
	namespace := *changedDescriptor.MustNamespace(f.schema)
	namespace.ObjectRevision++
	changedDescriptor.SetDescriptor(f.schema, &namespace)
	if candidate.InitialPrivilegesUnchanged(changedDescriptor) {
		t.Fatal("initial-only update ignored its object's predecessor")
	}
	journal := session.NewJournal()
	journal.RecordCandidateDefaults(published, &candidate)
	view := journal.PrivateRead(&f.e.Catalog.Owner, published)
	if view.State().InitialPrivilege(f.schema) == published.InitialPrivilege(f.schema) {
		t.Fatal("private read ignored the initial-only delta")
	}
	savepoint := journal.Clone()
	candidate.RemoveInitialPrivileges(f.schema)
	journal.RecordCandidateDefaults(published, &candidate)
	if journal.PrivateRead(&f.e.Catalog.Owner, published).State().InitialPrivilege(f.schema) != nil || savepoint.PrivateRead(&f.e.Catalog.Owner, published).State().InitialPrivilege(f.schema) == nil || view.State().InitialPrivilege(f.schema) == nil {
		t.Fatal("savepoint or cached private read lost immutable history")
	}
	if savepoint.InitialOriginals[f.schema] != published.InitialPrivilege(f.schema) {
		t.Fatal("savepoint did not retain the publication predecessor")
	}
	if !catalog.InitialPrivilegesUnchanged(published, savepoint.InitialOriginals) {
		t.Fatal("unchanged journal predecessor conflicted")
	}
	merge := session.NewJournal()
	if err := merge.MergeCandidate(published, published, &candidate); err != nil || merge.PrivateRead(&f.e.Catalog.Owner, published).State().InitialPrivilege(f.schema) != nil || merge.InitialOriginals[f.schema] != published.InitialPrivilege(f.schema) {
		t.Fatal("candidate merge lost initial-only tombstone or observation", err)
	}
	stale := session.NewJournal()
	if err := stale.MergeCandidate(base.State(), published, &candidate); err == nil || len(stale.InitialPrivileges) != 0 || len(stale.Descriptors) != 0 {
		t.Fatal("failed merge recorded a partial history", err)
	}
	broken := f.e.BeginCatalogStage(f.e.CatalogRead())
	defer f.e.DiscardCatalogStage(broken)
	broken.RecordInitialPrivilege(f.schema, 'e', authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}})
	if err := defaultUnitPublish(t, f.e, broken); err == nil || f.e.Catalog.State != published || f.e.Catalog.NextOID() != frontier {
		t.Fatal("invalid initial ACL published a partial root", err)
	}
	t.Run("repeated_private_history", pgInitPrivsRepeatedPrivateHistory)
}

func pgInitPrivsColumnRetirement(t *testing.T) {
	f := newDefaultUnitFixture(t)
	base := f.e.CatalogRead()
	typ, ok := base.LookupType(23)
	if !ok {
		t.Fatal("missing int4")
	}
	relation := &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000},
		ObjectNamespace: f.schema, ObjectName: "ip_sparse", ObjectRevision: 1,
		RelationKind: catalog.TableRelation, Owner: f.owner,
		Columns:    []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}},
		Attributes: []int32{1, 3}, Index: map[string]int{"a": 0, "b": 1},
	}
	candidate := catalog.NewCandidate(base)
	if err := candidate.Create(relation); err != nil {
		t.Fatal(err)
	}
	column := relation.Address()
	column.SubID = 3
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: authorization.SelectPrivilege}}}
	candidate.RecordInitialPrivilege(column, 'e', acl)
	candidate.RecordInitialPrivilege(relation.Address(), 'e', acl)
	retained := candidate.State()
	next := *relation
	next.ObjectRevision++
	next.Columns = next.Columns[:1]
	next.Attributes = next.Attributes[:1]
	next.Index = map[string]int{"a": 0}
	retirement := catalog.NewCandidate(base.WithState(retained))
	if err := retirement.Replace(&next); err != nil {
		t.Fatal(err)
	}
	result := retirement.State()
	if result.InitialPrivilege(column) != nil || result.InitialPrivilege(relation.Address()) != retained.InitialPrivilege(relation.Address()) || retained.InitialPrivilege(column) == nil {
		t.Fatal("relation replacement failed precise sparse column retirement")
	}
	if err := catalog.ValidateInitialPrivileges(f.e.Roles(), base.WithState(result)); err != nil {
		t.Fatal("valid remaining object snapshot rejected", err)
	}
}

func pgInitPrivsRepeatedPrivateHistory(t *testing.T) {
	f := newDefaultUnitFixture(t)
	base := f.e.CatalogRead()
	original := base.Get(f.schema)
	journal := session.NewJournal()
	firstACL := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{
		Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader},
		Privileges: authorization.UsagePrivilege,
	}}}
	change := func(history, current bool, acl authorization.ACLValue) {
		t.Helper()
		private := journal.PrivateRead(&f.e.Catalog.Owner, base.State())
		candidate := catalog.NewCandidate(private)
		next := *private.MustNamespace(f.schema)
		next.ObjectRevision++
		if current {
			next.ACL = acl
		}
		if err := candidate.Replace(&next); err != nil {
			t.Fatal(err)
		}
		if history {
			candidate.RecordInitialPrivilege(f.schema, 'e', acl)
		}
		capture := session.JournalCapture{Journal: journal, Candidate: &candidate, Committed: base.State(), Current: private.State()}
		if err := capture.ObserveCandidate(); err != nil {
			t.Fatal("private descriptor observation", err)
		}
		if err := journal.MergeCandidate(base.State(), private.State(), &candidate); err != nil {
			t.Fatal("private history merge", err)
		}
		f.e.Mu.Lock()
		err := f.e.CatalogPreflightLocked(journal)
		f.e.Mu.Unlock()
		if err != nil {
			t.Fatal("private writes became committed predecessor observations", err)
		}
		if journal.InitialOriginals[f.schema] != nil || journal.InitialReads[f.schema] != nil || journal.Originals[f.schema] != original {
			t.Fatal("repeated private change replaced committed absence or descriptor predecessor")
		}
	}
	change(true, false, firstACL)
	first := journal.PrivateRead(&f.e.Catalog.Owner, base.State()).State().InitialPrivilege(f.schema)
	savepoint := journal.Clone()
	change(true, false, authorization.ACLValue{})
	if journal.PrivateRead(&f.e.Catalog.Owner, base.State()).State().InitialPrivilege(f.schema) != nil {
		t.Fatal("private removal lost tombstone")
	}
	changedACL := authorization.ACLValue{Explicit: true, Entries: append([]authorization.ACLEntry(nil), firstACL.Entries...)}
	changedACL.Entries[0].Privileges |= authorization.CreatePrivilege
	change(false, true, changedACL)
	change(true, false, changedACL)
	retained := journal.PrivateRead(&f.e.Catalog.Owner, base.State())
	latest := retained.State().InitialPrivilege(f.schema)
	if latest == nil || latest == first || !authorization.EqualACL(latest.ACL, changedACL) {
		t.Fatal("re-add did not capture changed current ACL")
	}
	journal = savepoint
	restored := journal.PrivateRead(&f.e.Catalog.Owner, base.State()).State().InitialPrivilege(f.schema)
	if restored != first || !authorization.EqualACL(restored.ACL, firstACL) || retained.State().InitialPrivilege(f.schema) != latest || base.State().InitialPrivilege(f.schema) != nil {
		t.Fatal("savepoint restore changed historical backing or published absence")
	}
	f.e.Mu.Lock()
	err := f.e.CatalogPreflightLocked(journal)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal("restored history retained a private predecessor fence", err)
	}
}

func pgInitPrivsCanonicalBaseline(t *testing.T) {
	f := newDefaultUnitFixture(t)
	base := f.e.CatalogRead()
	baseline := maps.Collect(base.State().InitialPrivilegeBaseline())
	if len(baseline) == 0 || len(baseline) != base.State().InitialPrivilegeBaselineCount() || len(baseline) != base.State().InitialPrivilegeCount() {
		t.Fatal("canonical baseline was not captured with the initial seed")
	}
	for address, record := range baseline {
		if record != base.State().InitialPrivilege(address) {
			t.Fatal("initial baseline did not share immutable seed records", address)
		}
	}
	assertBaseline := func(state *catalog.State) {
		t.Helper()
		if state.InitialPrivilegeBaselineCount() != len(baseline) {
			t.Fatal("state projection changed canonical baseline size")
		}
		for address, record := range state.InitialPrivilegeBaseline() {
			if baseline[address] != record {
				t.Fatal("state projection rewrote baseline record backing", address)
			}
		}
	}
	self := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3394}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	original := baseline[self]
	if original == nil || baseline[public] == nil {
		t.Fatal("missing canonical self/public seed")
	}
	changed := authorization.ACLValue{Explicit: true, Entries: append([]authorization.ACLEntry(nil), original.ACL.Entries...)}
	changed.Entries[0].Privileges &^= authorization.SelectPrivilege
	candidate := catalog.NewCandidate(base)
	candidate.RecordInitialPrivilege(self, 'e', changed)
	candidate.RemoveInitialPrivileges(public)
	private := candidate.State()
	if private.InitialPrivilege(self) == original || private.InitialPrivilege(public) != nil || base.State().InitialPrivilege(public) != baseline[public] {
		t.Fatal("history mutation was not isolated from immutable seed")
	}
	assertBaseline(private)
	assertBaseline(candidate.ApplyTo(base.State()))
	assertBaseline(catalog.CloneState(private))
	journal := session.NewJournal()
	journal.RecordCandidateDefaults(base.State(), &candidate)
	assertBaseline(journal.PrivateRead(&f.e.Catalog.Owner, base.State()).State())
	assertBaseline(journal.Clone().PrivateRead(&f.e.Catalog.Owner, base.State()).State())
	// Complete native history replacement changes only the live inventory.
	// Deleted baseline targets remain comparison provenance, not live authority.
	empty := catalog.CloneState(private)
	empty.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{})
	empty.RemoveDescriptor(public)
	assertBaseline(empty)
	if err := catalog.ValidateInitialPrivileges(f.e.Roles(), base.WithState(empty)); err != nil {
		t.Fatal("canonical provenance was validated as live historical inventory", err)
	}
	if access.InitialRoleDependencies(base.WithState(empty), original.ACL.Entries[0].Grantor, nil) {
		t.Fatal("canonical provenance became a live initial ACL role dependency")
	}
	if base.State().InitialPrivilege(self) != original {
		t.Fatal("private baseline exercise changed published historical backing")
	}
}

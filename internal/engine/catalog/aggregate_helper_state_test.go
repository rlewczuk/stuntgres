package catalog

import (
	"stuntgres/internal/engine/ids"
	"testing"
)

func aggregateHelperUnitProcedure(oid ids.OID, namespace ids.ObjectAddress, name string) *Procedure {
	return &Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectName: name, ObjectRevision: 1, Signature: "2281", Result: 1007, RoutineKind: FunctionRoutine}
}

// Support aliases now use the ordinary descriptor/name/state owners. No second
// helper index or fake extension implementation supplies lookup authority.
func TestCatalogAggregateHelperDescriptorsFollowStateWrites(t *testing.T) {
	base, namespace, _ := catalogReadFixture()
	helper := aggregateHelperUnitProcedure(1001, namespace.Address(), "final_alias")
	base.SetDescriptor(helper.Address(), helper)
	base.BindName(DescriptorNameKey(helper), helper.Address())
	old := CloneState(base)
	if got, ok := old.Procedure(helper.Address()); !ok || got != helper || old.NameAddress(DescriptorNameKey(helper)) != helper.Address() {
		t.Fatal("helper is not an ordinary procedure descriptor")
	}
	base.RemoveDescriptor(helper.Address())
	base.UnbindName(DescriptorNameKey(helper))
	if _, ok := base.Procedure(helper.Address()); ok {
		t.Fatal("removed helper survived current root")
	}
	if got, ok := old.Procedure(helper.Address()); !ok || got != helper {
		t.Fatal("removal mutated retained descriptor")
	}
	old.ResetDescriptors()
	if _, ok := old.Procedure(helper.Address()); ok {
		t.Fatal("reset retained helper descriptor")
	}
}
func TestCatalogAggregateHelperDescriptorsFollowBulkLoads(t *testing.T) {
	base, namespace, _ := catalogReadFixture()
	helper := aggregateHelperUnitProcedure(2010, namespace.Address(), "final_alias")
	base.BeginBulkLoad(16)
	base.SetDescriptor(helper.Address(), helper)
	base.BindName(DescriptorNameKey(helper), helper.Address())
	base.EndBulkLoad()
	if got, ok := base.Procedure(helper.Address()); !ok || got != helper {
		t.Fatal("bulk load lost helper")
	}
	previous := base.ResetMaps()
	if _, ok := base.Procedure(helper.Address()); ok {
		t.Fatal("map reset retained helper")
	}
	base.BeginBulkLoad(16)
	for address, descriptor := range previous {
		base.SetDescriptor(address, descriptor)
	}
	base.EndBulkLoad()
	if got, ok := base.Procedure(helper.Address()); !ok || got != helper {
		t.Fatal("bulk refill lost helper")
	}
}
func TestCatalogAggregateHelperDescriptorsFollowCandidates(t *testing.T) {
	base, namespace, _ := catalogReadFixture()
	old := aggregateHelperUnitProcedure(3011, namespace.Address(), "old_alias")
	removed := aggregateHelperUnitProcedure(3021, namespace.Address(), "removed_alias")
	for _, helper := range []*Procedure{old, removed} {
		base.SetDescriptor(helper.Address(), helper)
		base.BindName(DescriptorNameKey(helper), helper.Address())
	}
	candidate := NewCandidate(NewRead(nil, base))
	created := aggregateHelperUnitProcedure(3031, namespace.Address(), "created_alias")
	if err := candidate.Create(created); err != nil {
		t.Fatal(err)
	}
	replacement := *old
	replacement.ObjectRevision++
	replacement.ObjectName = "renamed_alias"
	if err := candidate.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	candidate.RecordRemoval(ids.ObjectRef{Address: removed.Address()}, removed)
	next := candidate.State()
	if got, ok := next.Procedure(old.Address()); !ok || got != &replacement {
		t.Fatal("candidate lost helper replacement")
	}
	if _, ok := next.Procedure(removed.Address()); ok {
		t.Fatal("candidate retained removed helper")
	}
	if got, ok := next.Procedure(created.Address()); !ok || got != created {
		t.Fatal("candidate lost created helper")
	}
	if got, ok := base.Procedure(old.Address()); !ok || got != old {
		t.Fatal("candidate changed retained base")
	}
	applied := candidate.ApplyTo(CloneState(base))
	if got, ok := applied.Procedure(old.Address()); !ok || got != &replacement {
		t.Fatal("publication lost helper replacement")
	}
}
func TestSepgsqlSimulationLabels(t *testing.T) {
	base, namespace, _ := catalogReadFixture()
	helper := aggregateHelperUnitProcedure(4011, namespace.Address(), "labelled_alias")
	base.SetDescriptor(helper.Address(), helper)
	base.BindName(DescriptorNameKey(helper), helper.Address())
	key := SecurityLabelKey{Object: helper.Address(), Provider: "selinux"}
	original := &SecurityLabelRecord{Label: "old", PolicyID: "test", PolicyVersion: "1"}
	base.SetSecurityLabel(key, original)
	retained := CloneState(base)
	candidate := NewCandidate(NewRead(nil, base))
	candidate.ObserveSecurityLabel(key)
	incoming := &SecurityLabelRecord{Label: "new", PolicyID: "test", PolicyVersion: "1"}
	candidate.RecordSecurityLabel(key, incoming)
	incoming.Label = "caller-mutated"
	next := candidate.State()
	if next.SecurityLabel(key).Label != "new" || base.SecurityLabel(key) != original || retained.SecurityLabel(key) != original {
		t.Fatal("candidate borrowed label record or mutated retained state")
	}
	if !candidate.SecurityLabelsUnchanged(base) {
		t.Fatal("unchanged predecessor rejected")
	}
	conflict := CloneState(base)
	copy := *original
	conflict.SetSecurityLabel(key, &copy)
	if candidate.SecurityLabelsUnchanged(conflict) {
		t.Fatal("equal-text replacement supplied predecessor authority")
	}
	removed := NewCandidate(NewRead(nil, next))
	removed.RecordRemoval(ids.ObjectRef{Address: helper.Address()}, helper)
	if removed.State().SecurityLabel(key) != nil || next.SecurityLabel(key) == nil {
		t.Fatal("descriptor removal did not retire label atomically")
	}
	applied := candidate.ApplyTo(CloneState(base))
	if applied.SecurityLabel(key).Label != "new" || retained.SecurityLabel(key).Label != "old" {
		t.Fatal("label publication lost immutable backing")
	}
}

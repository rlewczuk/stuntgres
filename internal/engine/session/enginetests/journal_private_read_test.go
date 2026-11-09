package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func journalInitialPrivilegeACL() authorization.ACLValue {
	domain := &authorization.Domain{}
	roles := authorization.Seed(domain)
	owner := roles.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
}

func journalTestSchema(id ids.OID, name string) *catalog.Namespace {
	return &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: id}, ObjectName: name}
}

func journalTestState(descriptors ...catalog.Descriptor) *catalog.State {
	state := catalog.NewState(0)
	for _, descriptor := range descriptors {
		state.SetDescriptor(descriptor.Address(), descriptor)
		state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	return state
}

func TestJournalClonePrivateRead(t *testing.T) {
	owner := &catalog.Owner{Domain: &ids.Domain{}}
	schema := journalTestSchema(20001, "s")
	base := journalTestState(schema)
	base.SetEpoch(1)
	parent := session.NewJournal()
	next := *schema
	next.ObjectRevision++
	next.ObjectName = "private_schema"
	parent.RecordDescriptor(base, schema.Address(), &next)
	view := parent.PrivateRead(owner, base)
	version := view.CaptureDescriptorVersion()
	child := parent.Clone()
	if child.CachedBase != base || child.Cached.State() != view.State() || child.PrivateRead(owner, base).State() != view.State() {
		t.Fatal("clone discarded the immutable nonempty private view")
	}
	other := &catalog.Owner{Domain: owner.Domain}
	rebound := child.PrivateRead(other, base)
	if rebound.Owner() != other || rebound.State() != view.State() || parent.PrivateRead(owner, base).Owner() != owner {
		t.Fatal("cached read lost actual caller owner or changed parent authority")
	}
	for a := range version.DescriptorsToReconcile(parent.PrivateRead(owner, base)) {
		t.Fatal("unchanged parent lost exact backing", a)
	}
	for a := range rebound.CaptureDescriptorVersion().DescriptorsToReconcile(child.PrivateRead(other, base)) {
		t.Fatal("owner rebinding rebuilt immutable backing", a)
	}
	child.RecordDescriptor(base, schema.Address(), nil)
	removed := child.PrivateRead(owner, base)
	if removed.Get(schema.Address()) != nil || parent.PrivateRead(owner, base).Get(schema.Address()) != &next || version.Get(schema.Address()) != &next {
		t.Fatal("child tombstone altered retained parent generation")
	}
	grandchild := child.Clone()
	grandchild.RecordDescriptor(base, schema.Address(), schema)
	if grandchild.PrivateRead(owner, base).Get(schema.Address()) != schema || removed.Get(schema.Address()) != nil || parent.Cached.State() != view.State() {
		t.Fatal("rollback/reappend changed saved child or parent cached generation")
	}
	newBase := catalog.CloneState(base)
	if parent.PrivateRead(owner, newBase).State() == view.State() || view.Get(schema.Address()) != &next {
		t.Fatal("committed base replacement reused stale cached projection")
	}
}

func TestJournalPrivateReadInputInvalidation(t *testing.T) {
	for _, family := range []string{"descriptor", "default_acl", "initial_privilege", "security_label", "parameter"} {
		t.Run(family, func(t *testing.T) {
			owner := &catalog.Owner{Domain: &ids.Domain{}}
			schema := journalTestSchema(20001, "s")
			base := journalTestState(schema)
			base.SetEpoch(1)
			parent := session.NewJournal()
			// Ensure a genuinely nonempty private projection, not the empty fast path.
			anchor := journalTestSchema(20002, "anchor")
			parent.RecordDescriptor(base, anchor.Address(), anchor)
			before := parent.PrivateRead(owner, base)
			child := parent.Clone()
			key := catalog.DefaultACLKey{Class: authorization.DefaultTypes}
			label := catalog.SecurityLabelKey{Object: schema.Address(), Provider: "selinux"}
			acl := &catalog.DefaultACLRecord{}
			security := &catalog.SecurityLabelRecord{Label: "new"}
			switch family {
			case "descriptor":
				child.RecordDescriptor(base, schema.Address(), nil)
			case "default_acl":
				child.RecordDefaultACL(base, key, acl)
			case "initial_privilege":
				candidate := catalog.NewCandidate(before)
				candidate.RecordInitialPrivilege(schema.Address(), 'e', journalInitialPrivilegeACL())
				child.RecordCandidateInitialPrivileges(base, &candidate)
			case "security_label":
				child.RecordSecurityLabel(base, label, security)
			case "parameter":
				child.ParameterWritten = true
				child.ParameterIncarnation = base.ParameterIncarnation() + 1
				child.InvalidatePrivateRead()
			}
			if child.CachedBase != nil || child.Cached.State() != nil {
				t.Fatal("input writer retained a stale inherited private read")
			}
			after := child.PrivateRead(owner, base)
			if after.State() == before.State() || parent.PrivateRead(owner, base).State() != before.State() {
				t.Fatal("invalidated child projection reused parent state or mutated parent")
			}
			switch family {
			case "descriptor":
				if after.Get(schema.Address()) != nil || before.Get(schema.Address()) != schema {
					t.Fatal("descriptor invalidation lost tombstone")
				}
			case "default_acl":
				if after.State().DefaultACL(key) != acl || before.State().DefaultACL(key) != nil {
					t.Fatal("default ACL invalidation lost change")
				}
			case "initial_privilege":
				if after.State().InitialPrivilege(schema.Address()) == nil || before.State().InitialPrivilege(schema.Address()) != nil {
					t.Fatal("initial privilege invalidation lost change")
				}
			case "security_label":
				if after.State().SecurityLabel(label) != security || before.State().SecurityLabel(label) != nil {
					t.Fatal("label invalidation lost change")
				}
			case "parameter":
				if after.State().ParameterIncarnation() != base.ParameterIncarnation()+1 || before.State().ParameterIncarnation() != base.ParameterIncarnation() {
					t.Fatal("parameter invalidation lost incarnation")
				}
			}
			// A deliberately forged direct mutation must remain a forgery: do
			// not RecordDescriptor and silently repair its original/name fences.
			forged := child.Clone()
			forged.Descriptors[schema.Address()] = anchor
			forged.InvalidatePrivateRead()
			if forged.PrivateRead(owner, base).Get(schema.Address()) != anchor {
				t.Fatal("explicit invalidation did not expose the intentional direct tamper")
			}
			if forged.Originals[schema.Address()] != child.Originals[schema.Address()] || len(forged.Originals) != len(child.Originals) || len(forged.Names) != len(child.Names) || before.Get(schema.Address()) != schema {
				t.Fatal("explicit tamper invalidation repaired forged provenance or mutated saved state")
			}
		})
	}
}

func TestJournalClonePrivateReadAllInputs(t *testing.T) {
	owner := &catalog.Owner{Domain: &ids.Domain{}}
	schema := journalTestSchema(20001, "s")
	base := journalTestState(schema)
	base.SetEpoch(1)
	anchor := journalTestSchema(20002, "private")
	key := catalog.DefaultACLKey{Class: authorization.DefaultTypes}
	labelKey := catalog.SecurityLabelKey{Object: schema.Address(), Provider: "selinux"}
	defaultACL := &catalog.DefaultACLRecord{}
	label := &catalog.SecurityLabelRecord{Label: "parent"}
	parent := session.NewJournal()
	parent.RecordDescriptor(base, anchor.Address(), anchor)
	parent.RecordDefaultACL(base, key, defaultACL)
	candidate := catalog.NewCandidate(catalog.NewRead(owner, base))
	candidate.RecordInitialPrivilege(schema.Address(), 'e', journalInitialPrivilegeACL())
	parent.RecordCandidateInitialPrivileges(base, &candidate)
	parent.RecordSecurityLabel(base, labelKey, label)
	parent.ParameterWritten, parent.ParameterIncarnation = true, 7
	parent.InvalidatePrivateRead()
	view := parent.PrivateRead(owner, base)
	initial := view.State().InitialPrivilege(schema.Address())
	if initial == nil {
		t.Fatal("nonempty initial ACL did not create a real private record")
	}
	child := parent.Clone()
	if child.PrivateRead(owner, base).State() != view.State() || !child.ParameterWritten || child.ParameterIncarnation != 7 ||
		child.Descriptors[anchor.Address()] != anchor || child.DefaultACLs[key] != defaultACL || child.InitialPrivileges[schema.Address()] != initial || child.SecurityLabels[labelKey] != label {
		t.Fatal("clone did not copy every private-read input before sharing its immutable view")
	}
	child.RecordDescriptor(base, anchor.Address(), nil)
	child.RecordDefaultACL(base, key, nil)
	next := catalog.NewCandidate(view)
	next.RecordInitialPrivilege(schema.Address(), 'e', authorization.ACLValue{})
	child.RecordCandidateInitialPrivileges(base, &next)
	child.RecordSecurityLabel(base, labelKey, nil)
	child.ParameterIncarnation = 8
	child.InvalidatePrivateRead()
	current := child.PrivateRead(owner, base)
	if current.Get(anchor.Address()) != nil || current.State().DefaultACL(key) != nil || current.State().SecurityLabel(labelKey) != nil ||
		current.State().InitialPrivilege(schema.Address()) == initial || current.State().ParameterIncarnation() != 8 {
		t.Fatal("child projection lost real changes after inherited-cache invalidation")
	}
	if parent.PrivateRead(owner, base).State() != view.State() || view.Get(anchor.Address()) != anchor || view.State().DefaultACL(key) != defaultACL ||
		view.State().InitialPrivilege(schema.Address()) != initial || view.State().SecurityLabel(labelKey) != label || view.State().ParameterIncarnation() != 7 ||
		parent.Descriptors[anchor.Address()] != anchor || parent.DefaultACLs[key] != defaultACL || parent.InitialPrivileges[schema.Address()] != initial || parent.SecurityLabels[labelKey] != label {
		t.Fatal("child changed aliased parent maps, parameter flags or saved immutable projection")
	}
}

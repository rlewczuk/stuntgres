package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func tablespaceLifecycleState() *State {
	state := NewState(1)
	InstallBuiltinTablespaces(state, (&authorization.Domain{}).Principal(10))
	return state
}

func TestTablespaceLifecycleProviderAuthority(t *testing.T) {
	state := tablespaceLifecycleState()
	address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: DefaultTablespaceOID}
	old := state.Get(address).(*Tablespace)
	next := *old
	next.ObjectName, next.ObjectRevision = "renamed builtin", 2
	candidate := NewCandidate(Read{state: state})
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}
	renamed := candidate.ApplyTo(state)
	view := Read{state: renamed}
	if err := (TablespaceProvider{}).Validate(view, &next); err != nil {
		t.Fatal("renamed builtin rejected", err)
	}
	if renamed.NameAddress(DescriptorNameKey(old)) != (ids.ObjectAddress{}) || renamed.NameAddress(DescriptorNameKey(&next)) != address || state.Get(address) != old || state.NameAddress(DescriptorNameKey(old)) != address {
		t.Fatal("rename failed to rebind or mutated retained root")
	}
	foreign := next
	if err := (TablespaceProvider{}).Validate(view, &foreign); err == nil {
		t.Fatal("foreign descriptor pointer accepted")
	}
	if err := (TablespaceProvider{}).Validate(Read{}, &next); err == nil {
		t.Fatal("missing authority accepted")
	}
	for _, kind := range []string{"oid", "class", "subobject", "revision", "binding"} {
		t.Run(kind, func(t *testing.T) {
			bad := next
			switch kind {
			case "oid":
				bad.ObjectAddress.ObjectID = 90001
			case "class":
				bad.ObjectAddress.ClassID = ids.DatabaseClassID
			case "subobject":
				bad.ObjectAddress.SubID = 1
			case "revision":
				bad.ObjectRevision = 0
			}
			root := CloneState(renamed)
			root.SetDescriptor(bad.Address(), &bad)
			root.BindName(DescriptorNameKey(&bad), bad.Address())
			if kind == "binding" {
				root.UnbindName(DescriptorNameKey(&bad))
			}
			if err := (TablespaceProvider{}).Validate(Read{state: root}, &bad); err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}

func TestTablespaceLifecycleSharedProjectionAndAttachment(t *testing.T) {
	origin := &Store{State: tablespaceLifecycleState()}
	peer := &Store{State: CloneState(origin.State)}
	retained := peer.State
	address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: GlobalTablespaceOID}
	old := origin.State.Get(address).(*Tablespace)
	next := *old
	next.ObjectName, next.ObjectRevision = "shared renamed", 2
	next.ACL = authorization.ACLValue{Explicit: true}
	candidate := NewCandidate(origin.Current())
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}
	root := candidate.ApplyTo(origin.State)
	stores := func(yield func(*Store) bool) {
		if yield(origin) {
			yield(peer)
		}
	}
	projection := origin.ProjectSharedLocked(root, stores)
	if len(projection) != 2 || peer.State != retained || origin.State.Get(address) != old {
		t.Fatal("projection installed before atomic publication")
	}
	projection.InstallLocked()
	for _, store := range []*Store{origin, peer} {
		if store.State.Get(address) != &next || store.State.NameAddress(DescriptorNameKey(old)) != (ids.ObjectAddress{}) || store.State.NameAddress(DescriptorNameKey(&next)) != address {
			t.Fatal("peer lost shared immutable descriptor or live name")
		}
		if store.NextRevision() <= next.Revision() {
			t.Fatal("peer allocator can reuse a received shared revision")
		}
	}
	if retained.Get(address) != old || retained.NameAddress(DescriptorNameKey(old)) != address {
		t.Fatal("publication mutated retained peer image")
	}
	if projection := origin.ProjectSharedLocked(origin.State, stores); projection != nil {
		t.Fatal("unchanged publication allocated peer roots")
	}
	attached := tablespaceLifecycleState()
	origin.InheritTablespacesLocked(attached)
	if attached.Get(address) != &next || attached.NameAddress(DescriptorNameKey(old)) != (ids.ObjectAddress{}) || attached.NameAddress(DescriptorNameKey(&next)) != address {
		t.Fatal("new attachment retained stale template seed")
	}
	// Database retention is also used by replacement. It must not overwrite
	// an intentional Reset/Restore tablespace image with destination state.
	replacement := tablespaceLifecycleState()
	origin.RetainDatabasesLocked(replacement)
	if replacement.Get(address).Name() != "pg_global" {
		t.Fatal("database retention clobbered replacement tablespaces")
	}
}

func TestRetainDatabasesPeerSecurityLabels(t *testing.T) {
	for _, selectedLabel := range []string{"explicit", "absent"} {
		for _, importedPeer := range []string{"missing", "different", "same"} {
			t.Run(selectedLabel+"/"+importedPeer, func(t *testing.T) {
				selected := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 91001}
				peer := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 91002}
				unlabeledPeer := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 91003}
				unknownPeer := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 91004}
				published := NewState(1)
				for _, database := range []*Database{
					{ObjectAddress: selected, ObjectRevision: 1, ObjectName: "retained_selected"},
					{ObjectAddress: peer, ObjectRevision: 1, ObjectName: "retained_peer"},
					{ObjectAddress: unlabeledPeer, ObjectRevision: 1, ObjectName: "retained_unlabeled"},
				} {
					published.SetDescriptor(database.Address(), database)
					published.BindName(DescriptorNameKey(database), database.Address())
				}
				store := &Store{Owner: Owner{Database: selected}, State: published}
				selectedKey := SecurityLabelKey{Object: selected, Provider: "selinux"}
				peerKey := SecurityLabelKey{Object: peer, Provider: "selinux"}
				absentKey := SecurityLabelKey{Object: unlabeledPeer, Provider: "selinux"}
				unknownKey := SecurityLabelKey{Object: unknownPeer, Provider: "selinux"}
				oldSelected := &SecurityLabelRecord{Label: "destination selected", PolicyID: "destination", PolicyVersion: "1"}
				oldPeer := &SecurityLabelRecord{Label: "destination peer", PolicyID: "destination", PolicyVersion: "1"}
				published.SetSecurityLabel(selectedKey, oldSelected)
				published.SetSecurityLabel(peerKey, oldPeer)
				before := store.Current()
				beforeLabels := published.SecurityLabelRoot()
				imported := NewState(1)
				foreign := &Database{ObjectAddress: unknownPeer, ObjectRevision: 1, ObjectName: "imported_peer"}
				imported.SetDescriptor(unknownPeer, foreign)
				imported.BindName(DescriptorNameKey(foreign), unknownPeer)
				candidateLabel := &SecurityLabelRecord{Label: "imported", PolicyID: "source", PolicyVersion: "2"}
				var expectedSelected *SecurityLabelRecord
				if selectedLabel == "explicit" {
					expectedSelected = candidateLabel
					imported.SetSecurityLabel(selectedKey, expectedSelected)
				}
				switch importedPeer {
				case "different":
					imported.SetSecurityLabel(peerKey, candidateLabel)
				case "same":
					imported.SetSecurityLabel(peerKey, oldPeer)
				}
				imported.SetSecurityLabel(absentKey, candidateLabel)
				imported.SetSecurityLabel(unknownKey, candidateLabel)
				// Retention must not widen into any local or other shared class.
				var otherKeys []SecurityLabelKey
				for _, class := range []ids.OID{ids.NamespaceClassID, ids.RelationClassID, ids.ProcedureClassID,
					ids.SubscriptionClassID, ids.ParameterClassID, ids.ReplicationOriginClassID, ids.TablespaceClassID} {
					key := SecurityLabelKey{Object: ids.ObjectAddress{ClassID: class, ObjectID: 92001}, Provider: "selinux"}
					imported.SetSecurityLabel(key, candidateLabel)
					otherKeys = append(otherKeys, key)
				}
				columnKey := SecurityLabelKey{Object: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 92001, SubID: -1}, Provider: "selinux"}
				imported.SetSecurityLabel(columnKey, candidateLabel)
				otherKeys = append(otherKeys, columnKey)
				// A different provider on the same peer is also destination-owned.
				otherProvider := SecurityLabelKey{Object: peer, Provider: "other"}
				published.SetSecurityLabel(otherProvider, oldPeer)
				before = store.Current()
				beforeLabels = published.SecurityLabelRoot()
				sourceLabels := imported.SecurityLabelRoot()
				next := CloneState(imported)
				store.RetainDatabasesLocked(next)
				if next.SecurityLabel(peerKey) != oldPeer || next.SecurityLabel(otherProvider) != oldPeer ||
					next.SecurityLabel(absentKey) != nil || next.SecurityLabel(unknownKey) != nil ||
					next.SecurityLabel(selectedKey) != expectedSelected {
					t.Fatal("peer retention lost exact destination records/absence or changed selected candidate")
				}
				for _, key := range otherKeys {
					if next.SecurityLabel(key) != candidateLabel {
						t.Fatal("database retention changed another class/subobject", key)
					}
				}
				for _, address := range []ids.ObjectAddress{selected, peer, unlabeledPeer} {
					descriptor := before.Get(address)
					if next.Get(address) != descriptor || next.NameAddress(DescriptorNameKey(descriptor)) != address {
						t.Fatal("database retention lost actual destination descriptor/name", address)
					}
				}
				if next.Get(unknownPeer) != nil || next.NameAddress(DescriptorNameKey(foreign)) != (ids.ObjectAddress{}) {
					t.Fatal("imported peer descriptor/name survived retention")
				}
				labels := next.SecurityLabelRoot()
				store.RetainDatabasesLocked(next)
				if next.SecurityLabelRoot() != labels {
					t.Fatal("repeated peer label composition allocated another label root")
				}
				if before.State().SecurityLabelRoot() != beforeLabels || before.State().SecurityLabel(selectedKey) != oldSelected ||
					before.State().SecurityLabel(peerKey) != oldPeer || before.State().SecurityLabel(absentKey) != nil ||
					imported.SecurityLabelRoot() != sourceLabels || imported.Get(unknownPeer) != foreign ||
					imported.SecurityLabel(selectedKey) != expectedSelected || imported.SecurityLabel(absentKey) != candidateLabel ||
					imported.SecurityLabel(unknownKey) != candidateLabel || *candidateLabel != (SecurityLabelRecord{Label: "imported", PolicyID: "source", PolicyVersion: "2"}) {
					t.Fatal("retention mutated retained source/destination roots or label records")
				}
			})
		}
	}
}

func TestTablespaceLifecycleSharedNameExchange(t *testing.T) {
	seed := tablespaceLifecycleState()
	setup := NewCandidate(Read{state: seed})
	for i, builtin := range BuiltinTablespaces {
		address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
		old := seed.Get(address).(*Tablespace)
		next := *old
		next.ObjectName = []string{"alias_a", "alias_b"}[i]
		next.ObjectRevision++
		if err := setup.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	state := setup.ApplyTo(seed)
	origin, peer := &Store{State: state}, &Store{State: CloneState(state)}
	retainedPeer := peer.State
	candidate := NewCandidate(origin.Current())
	for _, step := range []struct {
		oid  ids.OID
		name string
	}{{DefaultTablespaceOID, "temporary_alias"}, {GlobalTablespaceOID, "alias_a"}, {DefaultTablespaceOID, "alias_b"}} {
		address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: step.oid}
		old := candidate.Get(address)
		if old == nil {
			old = candidate.Base.Get(address)
		}
		next := *old.(*Tablespace)
		next.ObjectName = step.name
		next.ObjectRevision++
		if err := candidate.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	root := candidate.ApplyTo(state)
	stores := func(yield func(*Store) bool) {
		if yield(origin) {
			yield(peer)
		}
	}
	projection := origin.ProjectSharedLocked(root, stores)
	if len(projection) != 2 || origin.State != state || peer.State != retainedPeer {
		t.Fatal("name exchange was not prepared atomically for both stores")
	}
	projection.InstallLocked()
	attached := CloneState(state)
	origin.InheritTablespacesLocked(attached)
	for _, image := range []*State{origin.State, peer.State, attached} {
		for i, name := range []string{"alias_b", "alias_a"} {
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: BuiltinTablespaces[i].OID}
			if image.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: name}) != address || image.Get(address).Name() != name {
				t.Fatal("exchanged peer/attachment lost live binding", name)
			}
		}
		if image.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: "temporary_alias"}) != (ids.ObjectAddress{}) {
			t.Fatal("intermediate spelling escaped publication")
		}
	}
	for _, image := range []*State{seed, state, retainedPeer, root, origin.State, peer.State, attached} {
		if image.shared.Len() != len(BuiltinTablespaces) {
			t.Fatal("canonical mutation lost persistent shared entries")
		}
		for _, builtin := range BuiltinTablespaces {
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
			descriptor := image.Get(address)
			if shared, found := image.shared.Get(address); !found || shared != descriptor {
				t.Fatal("shared index diverged from authoritative descriptor", address)
			}
			if err := (TablespaceProvider{}).Validate(Read{state: image}, descriptor); err != nil {
				t.Fatal("name exchange invalidated immutable authority", err)
			}
			if seed.Get(address).Name() != builtin.Name {
				t.Fatal("alias setup changed retained seed")
			}
		}
	}
	for _, image := range []*State{state, retainedPeer} {
		for i, name := range []string{"alias_a", "alias_b"} {
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: BuiltinTablespaces[i].OID}
			if image.Get(address) != state.Get(address) || image.Get(address).Name() != name || image.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: name}) != address {
				t.Fatal("name exchange changed retained peer authority", name)
			}
		}
	}
}

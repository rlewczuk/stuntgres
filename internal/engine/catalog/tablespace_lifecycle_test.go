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

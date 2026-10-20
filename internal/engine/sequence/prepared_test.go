package sequence

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// preparedFixture is one catalog at epoch 2 with an ordinary sequence, the
// caller's TEMP sequence and a peer's TEMP sequence, without an engine.
type preparedFixture struct {
	owner                    *catalog.Owner
	state                    *catalog.State
	caller, peer             ids.SessionToken
	plain, temporary, others *catalog.Relation
	memory                   SessionMemory
	journal                  *int
}

func newPreparedFixture(t *testing.T) *preparedFixture {
	t.Helper()
	sessions := &ids.Domain{}
	f := &preparedFixture{owner: &catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}, caller: sessions.Session(1), peer: sessions.Session(2), journal: new(int)}
	f.state = catalog.NewState(2)
	public := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectName: "public", ObjectRevision: 1}
	callerTemp := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30001}, ObjectName: "pg_temp_1", ObjectRevision: 1, Temporary: f.caller}
	peerTemp := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30002}, ObjectName: "pg_temp_2", ObjectRevision: 1, Temporary: f.peer}
	relation := func(oid ids.OID, ns *catalog.Namespace, stamp ids.DefinitionRevision) *catalog.Relation {
		return NewRelation(RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, Namespace: ns.Address(), Name: "s", Revision: stamp})
	}
	f.plain, f.temporary, f.others = relation(40001, public, 5), relation(40002, callerTemp, 6), relation(40003, peerTemp, 7)
	for _, d := range []catalog.Descriptor{public, callerTemp, peerTemp, f.plain, f.temporary, f.others} {
		f.state.SetDescriptor(d.Address(), d)
	}
	return f
}

func (f *preparedFixture) observation() Observation {
	return Observation{Domain: f.owner.Domain, Read: catalog.NewRead(f.owner, f.state), Session: f.caller, Journal: f.journal, Path: Environment{Namespaces: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: 2200}}}, Memory: &f.memory}
}

// constant captures d as a literal at offset, resolved at epoch.
func (f *preparedFixture) constant(offset int, d *catalog.Relation, epoch ids.CatalogEpoch, temporary ids.SessionToken) Constant {
	return Constant{Offset: offset, Ref: f.owner.Domain.Ref(epoch, d.Address()), Temporary: temporary, Stamp: d.SequencePlanStamp}
}

func wantValid(t *testing.T, label string, analysis *Analysis, o Observation, want bool) {
	t.Helper()
	if got, err := analysis.Valid(o); err != nil || got != want {
		t.Fatalf("%s: valid %v, %v; want %v", label, got, err, want)
	}
}

func wantReplaced(t *testing.T, label string, err error) {
	t.Helper()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("%s: %v, want ErrCatalogReplaced", label, err)
	}
}

// Analysis.Valid reports whether a prepared literal group may keep its
// resolution: the session, private journal, namespace generation, effective
// path and every literal's definition stamp must be unchanged.
func TestSequenceAnalysisValid(t *testing.T) {
	f := newPreparedFixture(t)
	var empty *Analysis
	wantValid(t, "nil cell", empty, Observation{}, true)
	analysis := NewAnalysis(f.observation(), []Constant{f.constant(7, f.plain, 2, ids.SessionToken{}), f.constant(19, f.temporary, 2, f.caller)})
	if analysis.Session != f.caller || analysis.Private != JournalIdentity(f.journal) || len(analysis.Constants) != 2 || !SameEnvironment(analysis.Path, f.observation().Path) {
		t.Fatal("NewAnalysis record", analysis)
	}
	wantValid(t, "unchanged", &analysis, f.observation(), true)

	// The caller's TEMP literal fences a session change (see the fences
	// test); an ordinary group only asks for reanalysis.
	ordinary := NewAnalysis(f.observation(), analysis.Constants[:1])
	changes := map[string]func(*Observation){
		"session":   func(o *Observation) { o.Session = f.peer },
		"journal":   func(o *Observation) { o.Journal = new(int) },
		"path":      func(o *Observation) { o.Path = Environment{ImplicitTemp: true} },
		"namespace": func(o *Observation) { f.state.SetNamespaceGeneration(f.state.NamespaceGeneration() + 1) },
	}
	for label, change := range changes {
		generation := f.state.NamespaceGeneration()
		o := f.observation()
		change(&o)
		wantValid(t, label, &ordinary, o, false)
		f.state.SetNamespaceGeneration(generation)
	}
	wantValid(t, "restored", &analysis, f.observation(), true)

	// A local source event (TRUNCATE RESTART IDENTITY) raises the stamp of
	// the caller's view only; generator state never takes part.
	f.memory.DemandLocked(f.plain.Address()).MarkSourceEventLocked(9)
	wantValid(t, "source event", &analysis, f.observation(), false)
	f.memory = SessionMemory{}
	altered := *f.plain
	altered.SequencePlanStamp = 8
	f.state.SetDescriptor(altered.Address(), &altered)
	wantValid(t, "plan stamp", &analysis, f.observation(), false)
	f.state.SetDescriptor(f.plain.Address(), f.plain)

	// Missing numeric regclass input stays valid until opening, unless it had resolved.
	missing := Analysis{Constants: []Constant{{Ref: f.owner.Domain.Ref(2, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49999})}}, Path: analysis.Path, Private: analysis.Private, Session: analysis.Session, Namespace: analysis.Namespace}
	wantValid(t, "missing unresolved", &missing, f.observation(), true)
	missing.Constants[0].Stamp = 3
	wantValid(t, "missing resolved", &missing, f.observation(), false)
}

func TestSequenceAnalysisValidFences(t *testing.T) {
	f := newPreparedFixture(t)
	check := func(label string, constant Constant) error {
		analysis := NewAnalysis(f.observation(), []Constant{constant})
		_, err := analysis.Valid(f.observation())
		return err
	}
	foreign := f.constant(1, f.plain, 2, ids.SessionToken{})
	foreign.Ref = (&ids.Domain{}).Ref(2, f.plain.Address())
	wantReplaced(t, "foreign domain", check("foreign domain", foreign))
	wantReplaced(t, "peer temporary", check("peer temporary", f.constant(1, f.others, 2, f.peer)))
	// Native replacement keeps only the caller's exact live TEMP sequence.
	wantReplaced(t, "replaced ordinary", check("replaced ordinary", f.constant(1, f.plain, 1, ids.SessionToken{})))
	if err := check("replaced own temporary", f.constant(1, f.temporary, 1, f.caller)); err != nil {
		t.Fatal("replaced own temporary", err)
	}
	moved := *f.temporary
	moved.ObjectNamespace = f.plain.Namespace()
	f.state.SetDescriptor(moved.Address(), &moved)
	wantReplaced(t, "temporary moved out", check("temporary moved out", f.constant(1, f.temporary, 1, f.caller)))
}

// PinReplacement keeps an exact retained TEMP capture's original epoch across
// reanalysis and fences every other replaced capture.
func TestSequencePinReplacement(t *testing.T) {
	f := newPreparedFixture(t)
	if err := PinReplacement(2, f.caller, nil, nil); err != nil {
		t.Fatal("no previous analysis", err)
	}
	previous := &Analysis{Constants: []Constant{f.constant(3, f.plain, 2, ids.SessionToken{}), f.constant(9, f.temporary, 1, f.caller)}}
	current := []Constant{f.constant(3, f.plain, 2, ids.SessionToken{}), f.constant(9, f.temporary, 2, f.caller)}
	if err := PinReplacement(2, f.caller, previous, current); err != nil || current[1].Ref.Epoch != 1 || current[0].Ref.Epoch != 2 {
		t.Fatal("pinned", err, current[0].Ref.Epoch, current[1].Ref.Epoch)
	}
	cases := map[string]func() (ids.SessionToken, []Constant){
		"peer session":   func() (ids.SessionToken, []Constant) { return f.peer, []Constant{f.constant(9, f.temporary, 2, f.caller)} },
		"other address":  func() (ids.SessionToken, []Constant) { return f.caller, []Constant{f.constant(9, f.others, 2, f.caller)} },
		"other owner":    func() (ids.SessionToken, []Constant) { return f.caller, []Constant{f.constant(9, f.temporary, 2, f.peer)} },
		"offset missing": func() (ids.SessionToken, []Constant) { return f.caller, []Constant{f.constant(10, f.temporary, 2, f.caller)} },
	}
	for label, build := range cases {
		session, constants := build()
		wantReplaced(t, label, PinReplacement(2, session, &Analysis{Constants: previous.Constants[1:]}, constants))
	}
	ordinary := &Analysis{Constants: []Constant{f.constant(3, f.plain, 1, ids.SessionToken{})}}
	wantReplaced(t, "replaced ordinary", PinReplacement(2, f.caller, ordinary, []Constant{f.constant(3, f.plain, 2, ids.SessionToken{})}))
}

func TestSequenceTemporaryEpochValid(t *testing.T) {
	f := newPreparedFixture(t)
	read := catalog.NewRead(f.owner, f.state)
	if !TemporaryEpochValid(read, f.owner.Domain.Ref(1, f.temporary.Address()), f.caller) {
		t.Fatal("caller's live TEMP sequence")
	}
	for label, valid := range map[string]bool{
		"no token":     TemporaryEpochValid(read, f.owner.Domain.Ref(1, f.temporary.Address()), ids.SessionToken{}),
		"peer token":   TemporaryEpochValid(read, f.owner.Domain.Ref(1, f.temporary.Address()), f.peer),
		"ordinary":     TemporaryEpochValid(read, f.owner.Domain.Ref(1, f.plain.Address()), f.caller),
		"future epoch": TemporaryEpochValid(read, f.owner.Domain.Ref(3, f.temporary.Address()), f.caller),
		"zero epoch":   TemporaryEpochValid(read, f.owner.Domain.Ref(0, f.temporary.Address()), f.caller),
		"foreign":      TemporaryEpochValid(read, (&ids.Domain{}).Ref(1, f.temporary.Address()), f.caller),
	} {
		if valid {
			t.Errorf("%s accepted", label)
		}
	}
}

// DefinitionStamp is the plan stamp of a sequence, raised by the session's
// own source events, and the revision of any other descriptor.
func TestSequenceDefinitionStamp(t *testing.T) {
	f := newPreparedFixture(t)
	if DefinitionStamp(f.plain, nil) != 5 || DefinitionStamp(f.plain, &f.memory) != 5 {
		t.Fatal("plain stamp")
	}
	f.memory.DemandLocked(f.plain.Address()).MarkSourceEventLocked(11)
	if DefinitionStamp(f.plain, &f.memory) != 11 {
		t.Fatal("source event stamp")
	}
	namespace := f.state.Get(f.plain.Namespace())
	if DefinitionStamp(namespace, &f.memory) != namespace.Revision() {
		t.Fatal("non-sequence revision")
	}
}

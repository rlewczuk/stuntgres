package session

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"sync"
	"testing"
)

func subjectFixture(t *testing.T, domain *ids.Domain, id ids.SessionID) *SepgsqlSubject {
	t.Helper()
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{Policy: sepgsql.Policy{ID: "subject", Version: "1", Contexts: []string{"original", "pending", "trusted", "nested", "unlabeled"}, InitialContext: "original", UnlabeledContext: "unlabeled"}})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := NewSepgsqlSubject(domain.Session(id), policy, "original")
	if err != nil {
		t.Fatal(err)
	}
	return subject
}
func TestSepgsqlSimulationSubject(t *testing.T) {
	domain := new(ids.Domain)
	subject := subjectFixture(t, domain, 1)
	assert := func(want string) {
		t.Helper()
		current, generation := subject.Snapshot()
		if current != want || subject.Current() != want || generation == 0 {
			t.Fatalf("subject=%q generation=%d want=%q", current, generation, want)
		}
	}
	assert("original")
	baseline := subject.Mark()
	_, initialGeneration := subject.Snapshot()
	subject.Set("pending")
	assert("pending")
	subject.Finish(false)
	assert("original")
	subject.Set("pending")
	subject.Finish(true)
	assert("pending")
	subject.Set("original")
	outer := subject.Mark()
	subject.Set("nested")
	subject.Restore(outer)
	assert("original")
	subject.Set("pending")
	trusted := subject.Enter("trusted")
	assert("trusted")
	nested := subject.Enter("nested")
	assert("nested")
	nested.Leave()
	assert("trusted")
	// Pending state and lexical overrides are distinct: setcon inside a trusted
	// invocation must not replace the override or survive transaction rollback.
	subject.Set("original")
	assert("trusted")
	trusted.Leave()
	assert("original")
	subject.Restore(baseline)
	assert("original")
	subject.Finish(false)
	assert("pending")
	if subject.Original() != "original" {
		t.Fatal("original connection identity changed")
	}
	if _, generation := subject.Snapshot(); generation <= initialGeneration {
		t.Fatal("subject mutation did not fence generations")
	}
	if _, err := NewSepgsqlSubject(domain.Session(2), subject.policy, "missing"); err == nil {
		t.Fatal("unknown physical context admitted")
	}
	before := subject.Generation()
	first := subject.Enter("trusted")
	_, generation, parentStamp, depth := subject.PhaseSnapshot()
	if generation != before || parentStamp == 0 || depth != 1 {
		t.Fatal("lexical entry changed underlying subject generation")
	}
	second := subject.Enter("nested")
	_, generation, childStamp, depth := subject.PhaseSnapshot()
	if generation != before || childStamp == 0 || childStamp == parentStamp || depth != 2 {
		t.Fatal("nested invocation stamp")
	}
	second.Leave()
	_, generation, stamp, depth := subject.PhaseSnapshot()
	if generation != before || stamp != parentStamp || depth != 1 {
		t.Fatal("return did not restore lexical parent")
	}
	first.Leave()
	if subject.Generation() != before {
		t.Fatal("balanced return invalidated historical authority")
	}
	replacement := subject.Enter("trusted")
	func() {
		defer func() {
			if recover() == nil {
				t.Error("retired token popped another invocation at the same depth")
			}
		}()
		first.Leave()
	}()
	if subject.Current() != "trusted" {
		t.Fatal("retired token mutated current frame")
	}
	replacement.Leave()
	// Real independent physical owners and concurrent read snapshots: channels
	// delimit mutation phases; no sleep, shared mutable authority or detached work.
	other := subjectFixture(t, domain, 2)
	ready := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		other.Set("nested")
		close(ready)
		<-release
		other.Finish(false)
		close(done)
	}()
	<-ready
	assert("pending")
	if current, generation := other.Snapshot(); current != "nested" || generation == 0 {
		t.Error("independent snapshot", current, generation)
	}
	close(release)
	<-done
	group.Wait()
	if other.Current() != "original" || subject.Current() != "pending" {
		t.Fatal("cross-session subject leakage")
	}
}
func TestSepgsqlSimulationCleanup(t *testing.T) {
	domain := new(ids.Domain)
	subject := subjectFixture(t, domain, 1)
	subject.Set("pending")
	token := subject.Enter("trusted")
	if err := subject.EndSession(domain.Session(2), CleanClose); err == nil || subject.Current() != "trusted" {
		t.Fatal("foreign resource cleanup accepted or mutated owner", err)
	}
	if err := subject.EndSession(domain.Session(1), Terminated); err != nil {
		t.Fatal(err)
	}
	token.Leave()
	if subject.Current() != "" || subject.Original() != "" || len(subject.trusted) != 0 || subject.committed != "" || subject.pending != "" {
		t.Fatal("terminal subject storage retained")
	}
}

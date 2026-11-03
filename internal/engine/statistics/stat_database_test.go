package statistics

import (
	"stuntgres/internal/engine/ids"
	"testing"
	"time"
)

func statDatabaseKey(d *ids.Domain, oid ids.OID) Key {
	return Key{Object: d.Ref(0, ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: oid}), Category: Database}
}
func TestStatDatabaseProvider(t *testing.T) {
	var domain ids.Domain
	key := statDatabaseKey(&domain, 10)
	relation := Key{Object: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20})}
	var p Provider
	var a, b Writer
	at := time.Unix(100, 0)
	a.Add(&p, key, Fact{Database: DatabaseFact{Inserts: 2, Updates: 1, Deletes: 1, ReadTime: 5 * time.Millisecond, WriteTime: 2 * time.Millisecond}})
	b.Add(&p, key, Fact{Database: DatabaseFact{Inserts: 1}})
	a.Flush(&p, at, true)
	b.Flush(&p, at, true)
	if got := p.Read(key).Database; got.Inserts != 3 || got.Updates != 1 || got.Deletes != 1 {
		t.Fatal(got)
	}
	if got := p.Read(key).Database; got.ReadTime != 5*time.Millisecond || got.WriteTime != 2*time.Millisecond {
		t.Fatal("independent elapsed-event arithmetic", got)
	}
	p.Retire(relation)
	if got := p.Read(key).Database.Inserts; got != 3 {
		t.Fatal("relation retirement erased database", got)
	}
	if got := p.Read(statDatabaseKey(&domain, 11)).Database; got != (DatabaseFact{}) {
		t.Fatal("cross database leak", got)
	}
	var reader Reader
	reader.Configure("snapshot", 1)
	reader.Capture([]CaptureSource{{Provider: &p, Database: key}}, at)
	a.Add(&p, key, Fact{Database: DatabaseFact{Inserts: 4}})
	// Model only the future published-only database reset boundary: no generation
	// replacement, no pending-writer or retained-reader invalidation.
	p.mu.Lock()
	generation := p.generation
	p.facts[key] = Fact{Database: DatabaseFact{ResetAt: at.Add(time.Second)}}
	p.mu.Unlock()
	if got := reader.Read(&p, key, at).Database; got.Inserts != 3 || !got.ResetAt.IsZero() {
		t.Fatal("retained capture changed", got)
	}
	a.Flush(&p, at.Add(2*time.Second), true)
	if got := p.Read(key); got.Database.Inserts != 4 || got.Database.ResetAt != at.Add(time.Second) || got.Generation != generation {
		t.Fatal("published reset lost pending", got)
	}
	reader.Clear()
	if got := reader.Read(&p, key, at.Add(2*time.Second)).Database; got.Inserts != 4 || got.ResetAt.IsZero() {
		t.Fatal(got)
	}
}
func TestStatDatabaseReader(t *testing.T) {
	var leftDomain, rightDomain ids.Domain
	left, right := statDatabaseKey(&leftDomain, 10), statDatabaseKey(&rightDomain, 11)
	var a, b Provider
	var leftWriter, rightWriter Writer
	at := time.Unix(200, 0)
	a.Reset(at)
	b.Reset(at.Add(time.Second))
	shared := statDatabaseKey(&rightDomain, 0)
	leftWriter.Add(&a, left, Fact{Database: DatabaseFact{Commits: 1}})
	leftWriter.Flush(&a, at, true)
	rightWriter.Add(&b, shared, Fact{Database: DatabaseFact{BlocksHit: 7}})
	rightWriter.Add(&b, right, Fact{Database: DatabaseFact{Commits: 2}})
	rightWriter.Flush(&b, at, true)
	var r Reader
	r.Configure("snapshot", 1)
	r.Capture([]CaptureSource{{Provider: &a, Database: left, All: true}, {Provider: &b, Database: right, Shared: shared}}, at)
	rightWriter.Add(&b, right, Fact{Database: DatabaseFact{Commits: 3}})
	rightWriter.Add(&b, shared, Fact{Database: DatabaseFact{BlocksHit: 5}})
	rightWriter.Flush(&b, at, true)
	if got := r.Read(&b, right, at).Database.Commits; got != 2 {
		t.Fatal("cross provider capture", got)
	}
	if got := r.Read(&b, shared, at).Database.BlocksHit; got != 7 {
		t.Fatal("shared fact was not captured with its provider", got)
	}
	missing := Key{Object: rightDomain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 99})}
	if got := r.Read(&b, missing, at).ResetAt; got != at.Add(time.Second) {
		t.Fatal("wrong provider missing default", got)
	}
	if r.Timestamp() != at {
		t.Fatal("capture timestamp", r.Timestamp())
	}
	r.Clear()
	r.Configure("none", 1)
	if got := r.Read(&b, right, at).Database.Commits; got != 5 {
		t.Fatal(got)
	}
}
func TestStatDatabaseDurations(t *testing.T) {
	at := time.Unix(300, 0)
	var s SessionUsage
	var total DatabaseFact
	s.Begin(at)
	total.Add(s.Transition(at.Add(2*time.Millisecond), true, false, true))
	total.Add(s.Transition(at.Add(7*time.Millisecond), false, true, true))
	total.Add(s.Transition(at.Add(13*time.Millisecond), true, false, true))
	total.Add(s.Transition(at.Add(16*time.Millisecond), false, false, true))
	total.Add(s.Transition(at.Add(20*time.Millisecond), false, false, false))
	total.Add(s.Transition(at.Add(20*time.Millisecond), false, false, false))
	if total.SessionTime != 20*time.Millisecond || total.ActiveTime != 8*time.Millisecond || total.IdleTransactionTime != 6*time.Millisecond {
		t.Fatal(total)
	}
	total.Add(s.Transition(at.Add(25*time.Millisecond), true, false, false))
	total.Add(s.Transition(at.Add(30*time.Millisecond), false, true, false))
	if total.SessionTime != 30*time.Millisecond || total.ActiveTime != 8*time.Millisecond || total.IdleTransactionTime != 6*time.Millisecond {
		t.Fatal("disabled interval reconstructed", total)
	}
}

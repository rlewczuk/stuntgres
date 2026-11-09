package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func replayDeltaRead(e *core.Engine, j *session.Journal) catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.PrivateCatalogLocked(j)
}

func replayDeltaGraph(e *core.Engine, s *session.Session, target ids.ObjectAddress) dml.TruncateGraph {
	j := s.Transactions.Catalog
	mark := j.DescriptorEditMark()
	current := replayDeltaRead(e, j)
	return dml.TruncateGraph{Base: current.CaptureDescriptorVersion(), ReceiptBase: e.CatalogRead().CaptureDescriptorVersion(), ReceiptMark: mark, Targets: map[ids.ObjectAddress]int{target: 1}, Token: s.SessionToken}
}

func replayDeltaWant(t *testing.T, g dml.TruncateGraph, e *core.Engine, j *session.Journal, mode dml.ReconcileMode, want map[ids.ObjectAddress]catalog.Descriptor, conflict bool) {
	t.Helper()
	current := replayDeltaRead(e, j)
	stream, gotMode := g.ReconcileDescriptors(current, e.CatalogRead(), j)
	got := make(map[ids.ObjectAddress]catalog.Descriptor)
	for a, d := range stream {
		got[a] = d
	}
	if gotMode != mode || !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciliation mode %v want %v; addresses/descriptors %v want %v", gotMode, mode, got, want)
	}
	err := g.CheckCurrent(current, e.CatalogRead(), j, e.Storage.Sequences)
	if conflict {
		if err != (catalog.Conflict{}) {
			t.Fatalf("missing graph conflict: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
}

// Compare independently frozen catalog inventories, not the journal edit stream.
// This includes implicit descriptors created by DDL as well as explicit objects.
func replayDeltaChanges(before, after catalog.Read) map[ids.ObjectAddress]catalog.Descriptor {
	result := make(map[ids.ObjectAddress]catalog.Descriptor)
	for a, d := range before.CaptureDescriptorVersion().DescriptorsToReconcile(after) {
		if before.Get(a) != d {
			result[a] = d
		}
	}
	return result
}

func replayDeltaInventory(current catalog.Read) map[ids.ObjectAddress]catalog.Descriptor {
	result := make(map[ids.ObjectAddress]catalog.Descriptor)
	var absent catalog.DescriptorVersion
	for a, d := range absent.DescriptorsToReconcile(current) {
		result[a] = d
	}
	return result
}

func TestTruncateReplayDeltaAccounting(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	tx := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, tx, false)
	var older []dml.TruncateGraph
	for _, query := range []string{"CREATE TABLE delta_table(id INT)", "ALTER TABLE delta_table ADD note TEXT", "CREATE INDEX delta_index ON delta_table(id)", "CREATE SCHEMA delta_schema"} {
		truncateReplayRetain(t, e, s, false, "p")
		g := replayDeltaGraph(e, s, p.Address)
		older = append(older, g)
		saved := s.Transactions.Catalog
		before := replayDeltaRead(e, saved)
		savedMark := saved.DescriptorEditMark()
		savedSerial := saved.DescriptorEditSerial()
		enginetest.SessionSQL(t, s, query)
		current := replayDeltaRead(e, s.Transactions.Catalog)
		want := replayDeltaChanges(before, current)
		if len(want) == 0 {
			t.Fatal("genuine DDL did not change descriptors", query)
		}
		replayDeltaWant(t, g, e, s.Transactions.Catalog, dml.ReconcileDelta, want, false)
		if !g.Base.Matches(before) || saved.DescriptorEditMark() != savedMark || saved.DescriptorEditSerial() != savedSerial || !replayDeltaRead(e, saved).CaptureDescriptorVersion().Matches(before) {
			t.Fatal("DDL mutated admitted baseline or saved journal")
		}
		fresh := replayDeltaGraph(e, s, p.Address)
		replayDeltaWant(t, fresh, e, s.Transactions.Catalog, dml.ReconcileExact, map[ids.ObjectAddress]catalog.Descriptor{}, false)
	}
	current := replayDeltaRead(e, s.Transactions.Catalog)
	for _, g := range older {
		stream, mode := g.ReconcileDescriptors(current, e.CatalogRead(), s.Transactions.Catalog)
		if mode != dml.ReconcileDelta {
			t.Fatal("older retained receipt lost delta authority", mode)
		}
		for a, d := range stream {
			if d != current.Get(a) {
				t.Fatal("delta did not fetch current descriptor")
			}
		}
		if err := g.CheckCurrent(current, e.CatalogRead(), s.Transactions.Catalog, e.Storage.Sequences); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTruncateReplayNonStageDelta(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	enginetest.SessionSQL(t, s, "CREATE TYPE delta_enum AS ENUM ('one')")
	tx := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, tx, false)
	truncateReplayRetain(t, e, s, false, "p")
	for _, query := range []string{"CREATE SEQUENCE delta_sequence", "ALTER TYPE delta_enum ADD VALUE 'two'", "GRANT SELECT ON q TO PUBLIC", "SELECT lo_create(930001::oid)"} {
		g := replayDeltaGraph(e, s, p.Address)
		saved := s.Transactions.Catalog
		before := replayDeltaRead(e, saved)
		mark := saved.DescriptorEditMark()
		serial := saved.DescriptorEditSerial()
		enginetest.SessionSQL(t, s, query)
		current := replayDeltaRead(e, s.Transactions.Catalog)
		want := replayDeltaChanges(before, current)
		if len(want) == 0 {
			t.Fatal("non-stage writer produced no descriptor change", query)
		}
		replayDeltaWant(t, g, e, s.Transactions.Catalog, dml.ReconcileDelta, want, false)
		if saved.DescriptorEditMark() != mark || saved.DescriptorEditSerial() != serial || !g.Base.Matches(before) {
			t.Fatal("non-stage writer mutated saved authority")
		}
		enginetest.SessionSQL(t, s, "CREATE TABLE stage_refresh_"+map[string]string{"CREATE SEQUENCE delta_sequence": "sequence", "ALTER TYPE delta_enum ADD VALUE 'two'": "enum", "GRANT SELECT ON q TO PUBLIC": "grant", "SELECT lo_create(930001::oid)": "largeobject"}[query]+"(id INT)")
		fresh := replayDeltaGraph(e, s, p.Address)
		replayDeltaWant(t, fresh, e, s.Transactions.Catalog, dml.ReconcileExact, map[ids.ObjectAddress]catalog.Descriptor{}, false)
	}
}

func TestTruncateReplayNamespaceDelta(t *testing.T) {
	for _, change := range []string{"sameTemporary", "changedTemporary", "removed"} {
		t.Run(change, func(t *testing.T) {
			e, s, p, _ := truncateUnitFixture(t)
			admitted := e.CatalogRead().CaptureDescriptorVersion()
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TEMP TABLE delta_peer() INHERITS(p)")
			tx := enginetest.TransactionBegin(t, s)
			defer enginetest.TransactionEnd(t, s, tx, false)
			truncateReplayRetain(t, e, s, false, "p")
			g := replayDeltaGraph(e, s, p.Address)
			// The original admission predates the peer hierarchy; a later complete
			// check admits its peer-TEMP exclusion without changing original Base.
			admittedGraph := g
			admittedGraph.Base = admitted
			admittedGraph.ReceiptBase = catalog.DescriptorVersion{}
			if err := admittedGraph.CheckCurrent(replayDeltaRead(e, s.Transactions.Catalog), e.CatalogRead(), s.Transactions.Catalog, e.Storage.Sequences); err != nil {
				t.Fatal(err)
			}
			g.Checked = g.Base
			g.Base = admitted
			saved := s.Transactions.Catalog
			before := replayDeltaRead(e, saved)
			j := saved.Clone()
			next := *before.MustNamespace(peer.TemporaryNamespace)
			next.ObjectRevision++
			next.ObjectName = "delta_peer_renamed"
			var descriptor catalog.Descriptor = &next
			mode, conflict := dml.ReconcileDelta, false
			if change == "changedTemporary" {
				next.Temporary = s.SessionToken
				mode, conflict = dml.ReconcileFull, true
			}
			if change == "removed" {
				descriptor = nil
				mode, conflict = dml.ReconcileFull, true
			}
			j.RecordDescriptor(e.CatalogRead().State(), peer.TemporaryNamespace, descriptor)
			current := replayDeltaRead(e, j)
			want := map[ids.ObjectAddress]catalog.Descriptor{peer.TemporaryNamespace: &next}
			if mode == dml.ReconcileFull {
				want = replayDeltaInventory(current)
			}
			replayDeltaWant(t, g, e, j, mode, want, conflict)
			if s.Transactions.Catalog != saved || g.Base != admitted || !g.Checked.Matches(before) || before.MustNamespace(peer.TemporaryNamespace).Temporary != peer.SessionToken {
				t.Fatal("candidate changed saved namespace authority")
			}
		})
	}
}

func TestTruncateReplayBaseChangeDelta(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	peer := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, tx, false)
	truncateReplayRetain(t, e, s, false, "p")
	enginetest.SessionSQL(t, s, "CREATE SCHEMA before_publication")
	g := replayDeltaGraph(e, s, p.Address)
	saved := s.Transactions.Catalog
	base := e.CatalogRead()
	enginetest.SessionSQL(t, peer, "CREATE SCHEMA published_elsewhere")
	if e.CatalogRead().Epoch() != base.Epoch() || g.ReceiptBase.Matches(e.CatalogRead()) {
		t.Fatal("publication did not replace same-epoch committed backing")
	}
	replayDeltaWant(t, g, e, saved, dml.ReconcileFull, replayDeltaInventory(replayDeltaRead(e, saved)), false)
	enginetest.SessionSQL(t, s, "CREATE SCHEMA after_publication")
	fresh := replayDeltaGraph(e, s, p.Address)
	replayDeltaWant(t, fresh, e, s.Transactions.Catalog, dml.ReconcileExact, map[ids.ObjectAddress]catalog.Descriptor{}, false)
	if g.ReceiptBase != base.CaptureDescriptorVersion() || g.ReceiptMark != saved.DescriptorEditMark() {
		t.Fatal("refresh rewrote saved receipt provenance")
	}
}

func TestTruncateReplayUntrustedJournalDelta(t *testing.T) {
	for _, kind := range []string{"inserted", "deleted", "foreignMark", "missingMark", "absentReceipt"} {
		t.Run(kind, func(t *testing.T) {
			e, s, p, q := truncateUnitFixture(t)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TEMP TABLE delta_incoming() INHERITS(p)")
			tx := enginetest.TransactionBegin(t, s)
			defer enginetest.TransactionEnd(t, s, tx, false)
			truncateReplayRetain(t, e, s, false, "p")
			g := replayDeltaGraph(e, s, p.Address)
			saved := s.Transactions.Catalog
			before := replayDeltaRead(e, saved)
			savedMark := saved.DescriptorEditMark()
			j := saved.Clone()
			// Reuse the genuine admitted peer hierarchy, but move its authority to the
			// persistent namespace in this deliberately invalid candidate.
			address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "delta_incoming"})
			edge := *before.MustRelation(address)
			edge.ObjectRevision++
			edge.ObjectNamespace = before.MustRelation(p.Address).Namespace()
			j.RecordDescriptor(e.CatalogRead().State(), address, &edge)
			switch kind {
			case "inserted":
				j.Descriptors[q.Address] = before.Get(q.Address)
			case "deleted":
				j.RecordDescriptor(e.CatalogRead().State(), q.Address, before.Get(q.Address))
				delete(j.Descriptors, q.Address)
			case "foreignMark":
				sibling := saved.Clone()
				sibling.RecordDescriptor(e.CatalogRead().State(), q.Address, before.Get(q.Address))
				g.ReceiptMark = sibling.DescriptorEditMark()
			case "missingMark":
				descendant := j.Clone()
				descendant.RecordDescriptor(e.CatalogRead().State(), q.Address, before.Get(q.Address))
				g.ReceiptMark = descendant.DescriptorEditMark()
			case "absentReceipt":
				g.ReceiptBase = catalog.DescriptorVersion{}
			}
			j.InvalidatePrivateRead()
			replayDeltaWant(t, g, e, j, dml.ReconcileFull, replayDeltaInventory(replayDeltaRead(e, j)), true)
			if saved.DescriptorEditMark() != savedMark || !g.Base.Matches(before) {
				t.Fatal("untrusted candidate mutated saved authority")
			}
		})
	}
}

func TestTruncateReplayForeignAuthorityDelta(t *testing.T) {
	for _, kind := range []string{"effectiveOwner", "effectiveEpoch", "uncertifiedOwner", "uncertifiedDomain", "uncertifiedEpoch", "currentOwner", "foreignPair"} {
		t.Run(kind, func(t *testing.T) {
			e, s, p, _ := truncateUnitFixture(t)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TEMP TABLE authority_incoming() INHERITS(p)")
			tx := enginetest.TransactionBegin(t, s)
			defer enginetest.TransactionEnd(t, s, tx, false)
			truncateReplayRetain(t, e, s, false, "p")
			g := replayDeltaGraph(e, s, p.Address)
			saved := s.Transactions.Catalog
			before := replayDeltaRead(e, saved)
			mark := saved.DescriptorEditMark()
			j := saved.Clone()
			address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "authority_incoming"})
			edge := *before.MustRelation(address)
			edge.ObjectRevision++
			edge.ObjectNamespace = before.MustRelation(p.Address).Namespace()
			base := e.CatalogRead()
			j.RecordDescriptor(base.State(), address, &edge)
			current := replayDeltaRead(e, j)
			foreign := *before.Owner()
			switch kind {
			case "effectiveOwner":
				g.Checked = catalog.NewRead(&foreign, before.State()).CaptureDescriptorVersion()
			case "effectiveEpoch", "uncertifiedEpoch":
				state := catalog.CloneState(before.State())
				epoch := before.Epoch() + 1
				if kind == "uncertifiedEpoch" {
					epoch = 0
				}
				state.SetEpoch(epoch)
				g.Checked = before.WithState(state).CaptureDescriptorVersion()
			case "uncertifiedOwner":
				g.Checked = catalog.NewRead(nil, before.State()).CaptureDescriptorVersion()
			case "uncertifiedDomain":
				foreign.Domain = nil
				g.Checked = catalog.NewRead(&foreign, before.State()).CaptureDescriptorVersion()
			case "currentOwner":
				current = catalog.NewRead(&foreign, current.State())
			case "foreignPair":
				current = catalog.NewRead(&foreign, current.State())
				g.Checked = current.CaptureDescriptorVersion()
			}
			stream, mode := g.ReconcileDescriptors(current, base, j)
			got := make(map[ids.ObjectAddress]catalog.Descriptor)
			for a, d := range stream {
				got[a] = d
			}
			if mode != dml.ReconcileFull || !reflect.DeepEqual(got, replayDeltaInventory(current)) {
				t.Fatal("foreign or uncertified authority borrowed a genuine edit mark and cached projection", mode)
			}
			if err := g.CheckCurrent(current, base, j, e.Storage.Sequences); err != (catalog.Conflict{}) {
				t.Fatal("foreign authority hid the real incoming hierarchy edge", err)
			}
			if saved.DescriptorEditMark() != mark || !g.Base.Matches(before) || before.MustRelation(address).Namespace() != peer.TemporaryNamespace {
				t.Fatal("rejected authority mutated the saved journal or admitted backing")
			}
		})
	}
}

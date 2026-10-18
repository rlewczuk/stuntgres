package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// Publish through the real host stage authority: these graph changes are the
// existing control-plane seam, not a pretend explicit-transaction SQL provider.
func truncateUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
func truncateUnitReplace(t *testing.T, e *Engine, ref ids.ObjectRef, change func(*relationDescriptor)) {
	t.Helper()
	base := e.CatalogRead()
	original := base.state.descriptors[ref.Address].(*relationDescriptor)
	next := *original
	next.revision++
	change(&next)
	stage := e.BeginCatalogStage(base)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	truncateUnitPublish(t, e, stage)
}
func truncateUnitFK(t *testing.T, e *Engine, parent, child ids.ObjectRef) {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	next := *base.state.descriptors[child.Address].(*relationDescriptor)
	next.revision++
	raw := parser.ForeignKeyConstraint{Columns: []parser.Identifier{{Text: "id"}}, Referenced: []parser.Identifier{{Text: "id"}}, Target: parser.ObjectName{Parts: []parser.Identifier{{Text: "p"}}}}
	e.mu.Lock()
	err := e.stageForeignKey(catalogAuthorization(e, base), stage, &next, raw, parent.Address, ids.ObjectAddress{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	truncateUnitPublish(t, e, stage)
}
func truncateUnitAsync(t *testing.T, e *Engine, s *Session, stmt *parser.Truncate) (<-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, err := s.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, stmt, 0, nil)
			op.Done()
		}
		done <- err
	}()
	// A separate completion signal lets cleanup join even if the result was
	// already consumed. Cancellation unwinds a held prefix before session cleanup.
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("truncate worker did not terminate")
		}
	})
	return done, cancel
}
func truncateUnitJoin(t *testing.T, done <-chan error, wantCode string) {
	t.Helper()
	select {
	case err := <-done:
		if wantCode == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var state *sqlstate.Error
			if !errors.As(err, &state) || state.Code != wantCode {
				t.Fatalf("error %v, want SQLSTATE %s", err, wantCode)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("truncate worker did not terminate")
	}
}

func TestTruncateClosure(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	for _, q := range []string{"CREATE TABLE c(id INT PRIMARY KEY REFERENCES p)", "CREATE TABLE g(id INT REFERENCES c)", "CREATE TABLE u(id INT PRIMARY KEY)", "CREATE TABLE outgoing(id INT REFERENCES u)"} {
		sessionSQL(t, s, q)
	}
	// c needs a real referenced key for the second edge; retain the ordinary
	// target-independent table as proof that closure walks incoming edges only.
	selected := map[ids.ObjectAddress]int{p.Address: 1}
	edges := truncateReferencers(e.CatalogRead(), selected)
	if len(edges) != 1 || edges[0].relation.name != "c" || edges[0].parent.name != "p" {
		t.Fatal("not relation-level incoming closure", edges)
	}
	c := roleTableRef(t, e, "c")
	selected[c.Address] = 2
	edges = truncateReferencers(e.CatalogRead(), selected)
	if len(edges) != 1 || edges[0].relation.name != "g" {
		t.Fatal("not next FK wave", edges)
	}
	selected[roleTableRef(t, e, "g").Address] = 3
	if len(truncateReferencers(e.CatalogRead(), selected)) != 0 {
		t.Fatal("closure includes unrelated/outgoing relations")
	}
}
func TestTruncateGraph(t *testing.T) {
	for _, kind := range []string{"foreign-key", "inheritance"} {
		t.Run(kind, func(t *testing.T) {
			e, s, p, _ := truncateUnitFixture(t)
			var inheritance *tableInheritance
			if kind == "inheritance" {
				sessionSQL(t, s, "CREATE TABLE c(extra INT) INHERITS(p)")
				c := roleTableRef(t, e, "c")
				inheritance = e.state.descriptors[c.Address].(*relationDescriptor).inheritance
				truncateUnitReplace(t, e, c, func(d *relationDescriptor) { d.inheritance = nil })
			} else {
				sessionSQL(t, s, "CREATE TABLE c(id INT)")
			}
			child := roleTableRef(t, e, "c")
			scope := truncateUnitStage(t, e, s, "p")
			old := e.storage[p.Address]
			if kind == "foreign-key" {
				truncateUnitFK(t, e, p, child)
			} else {
				truncateUnitReplace(t, e, child, func(d *relationDescriptor) { d.inheritance = inheritance })
			}
			if err := scope.Finish(nil); err != (CatalogConflict{}) {
				t.Fatalf("late host graph: %v", err)
			}
			scope.op.Done()
			if e.storage[p.Address] != old || old.truncateLineage != nil || len(old.versions) != 2 {
				t.Fatal("late graph veto changed rows")
			}
		})
	}
}
func TestTruncateGraphTemporarySessions(t *testing.T) {
	e, owner, parent, _ := truncateUnitFixture(t)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TEMP TABLE own_child() INHERITS(p)")
	sessionSQL(t, owner, "INSERT INTO own_child VALUES(2)")
	sessionSQL(t, owner, "CREATE TABLE persistent_child() INHERITS(p)")
	sessionSQL(t, owner, "INSERT INTO persistent_child VALUES(4)")
	sessionSQL(t, peer, "CREATE TEMP TABLE peer_child() INHERITS(p)")
	sessionSQL(t, peer, "INSERT INTO peer_child VALUES(3)")
	catalog := e.CatalogRead()
	ownAddress := catalog.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.temporaryNamespace, Name: "own_child"}]
	peerAddress := catalog.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.temporaryNamespace, Name: "peer_child"}]
	persistent := roleTableRef(t, e, "persistent_child")
	if ownAddress == (ids.ObjectAddress{}) || peerAddress == (ids.ObjectAddress{}) || ownAddress == peerAddress {
		t.Fatal("fixture did not create distinct session-owned temporary descendants")
	}
	peerRef := e.domain.Ref(catalog.Epoch(), peerAddress)
	oldPeer := e.storage[peerAddress]
	oldVersions, oldEnds, oldGeneration := oldPeer.versions, oldPeer.ends, oldPeer.generation
	scope := truncateUnitStage(t, e, owner, "p")
	_, stagedPeer := scope.catalog.storage[peerAddress]
	excluded := scope.truncateSelected[peerAddress] == 0 && !stagedPeer && len(scope.truncateRelations) == 3
	e.locks.mu.Lock()
	_, journaledPeer := scope.ticket.resources[lockResource{ObjectRef: peerRef}]
	excluded = excluded && !journaledPeer && e.locks.entries[lockResource{ObjectRef: peerRef}] == nil
	admitted := true
	for _, address := range []ids.ObjectAddress{parent.Address, ownAddress, persistent.Address} {
		ref := e.domain.Ref(catalog.Epoch(), address)
		entry := e.locks.entries[lockResource{ObjectRef: ref}]
		if entry == nil {
			admitted = false
			continue
		}
		grant, held := entry.holders[scope.ticket]
		admitted = admitted && held && grant.mode == accessExclusive && scope.truncateSelected[address] != 0
	}
	e.locks.mu.Unlock()
	if !excluded || !admitted {
		t.Fatalf("temporary descendant admission: foreign excluded=%v, own/persistent AX admitted=%v", excluded, admitted)
	}
	// A real host rename changes the excluded descriptor after acquisition.
	// Final hierarchy reconciliation must apply the same TEMP-token exclusion.
	truncateUnitReplace(t, e, peerRef, func(d *relationDescriptor) { d.name = "renamed_peer_child" })
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.op.Done()
	if e.storage[peerAddress] != oldPeer || !sameVersionRoot(oldPeer.versions, oldVersions) || oldPeer.ends != oldEnds || oldPeer.generation != oldGeneration || oldPeer.truncateLineage != nil {
		t.Fatal("foreign-session temporary storage was replaced or stamped")
	}
	transactionRows(t, peer, "SELECT id FROM renamed_peer_child", 3)
	transactionRows(t, owner, "SELECT id FROM own_child")
	transactionRows(t, owner, "SELECT id FROM persistent_child")
	transactionRows(t, owner, "SELECT id FROM p")
}
func TestTruncateLocks(t *testing.T) {
	e, holder, p, q := truncateUnitFixture(t)
	waiter := sessionStart(t, e, nil)
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "LOCK q IN ACCESS SHARE MODE")
	done, cancel := truncateUnitAsync(t, e, waiter, truncateUnitAST("p", "q"))
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	prefix := e.locks.entries[lockResource{ObjectRef: p}]
	blocked := e.locks.entries[lockResource{ObjectRef: q}]
	valid := prefix != nil && len(prefix.holders) == 1 && blocked != nil && len(blocked.pending) == 1
	if valid {
		request := blocked.pending[0]
		ticket := request.ticket
		_, prefixJournal := ticket.resources[lockResource{ObjectRef: p}]
		_, pendingJournal := ticket.resources[lockResource{ObjectRef: q}]
		grant, held := prefix.holders[ticket]
		valid = held && grant.mode == accessExclusive && request.mode == accessExclusive && request.ref == (lockResource{ObjectRef: q}) && request.provenance.Session() == waiter.token && e.locks.owners[request.provenance] == ticket && prefixJournal && pendingJournal
	}
	e.locks.mu.Unlock()
	if !valid {
		t.Fatal("missing real AX prefix/second-target pending request")
	}
	cancel()
	lockAwait(t, done, context.Canceled)
	e.locks.mu.Lock()
	prefix = e.locks.entries[lockResource{ObjectRef: p}]
	blocked = e.locks.entries[lockResource{ObjectRef: q}]
	clean := prefix == nil && blocked != nil && len(blocked.pending) == 0
	e.locks.mu.Unlock()
	if !clean || len(e.storage[p.Address].versions) != 2 || len(e.storage[q.Address].versions) != 1 {
		t.Fatal("cancellation retained prefix or changed roots")
	}
	transactionEnd(t, holder, tx, false)
	lockEmpty(t, e)
}
func TestTruncateAdmission(t *testing.T) {
	for _, kind := range []string{"name", "nontruncate-storage", "control-epoch"} {
		t.Run(kind, func(t *testing.T) {
			e, holder, p, _ := truncateUnitFixture(t)
			waiter := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK p IN ACCESS SHARE MODE")
			old := e.storage[p.Address]
			versions, ends := old.versions, old.ends
			done, _ := truncateUnitAsync(t, e, waiter, truncateUnitAST("p"))
			lockPending(t, e, 1)
			switch kind {
			case "name":
				truncateUnitReplace(t, e, p, func(d *relationDescriptor) { d.name = "renamed" })
			case "nontruncate-storage":
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				next := *base.state.descriptors[p.Address].(*relationDescriptor)
				next.revision++
				replacement := &relationStorage{versions: old.versions, ends: old.ends, generation: old.generation + 1}
				if err := stage.alterRelation(context.Background(), p, &next, old, replacement, len(old.versions), old.generation, old.ends); err != nil {
					t.Fatal(err)
				}
				truncateUnitPublish(t, e, stage)
			case "control-epoch":
				if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE p(id INT PRIMARY KEY); INSERT INTO p VALUES(9);")); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "control-epoch" {
				transactionEnd(t, holder, tx, false)
			}
			lockAwait(t, done, ErrCatalogReplaced)
			if kind == "control-epoch" {
				transactionEnd(t, holder, tx, false)
				transactionRows(t, waiter, "SELECT id FROM p", 9)
			}
			if old.truncateLineage != nil || !sameVersionRoot(old.versions, versions) || old.ends != ends {
				t.Fatal("rejected waiter stamped/mutated retired source")
			}
			lockEmpty(t, e)
		})
	}
	for _, only := range []bool{false, true} {
		t.Run(map[bool]string{false: "late-inheritance", true: "late-inheritance-only"}[only], func(t *testing.T) {
			e, holder, p, _ := truncateUnitFixture(t)
			sessionSQL(t, holder, "CREATE TABLE c(extra INT) INHERITS(p)")
			sessionSQL(t, holder, "INSERT INTO c(id,extra) VALUES(1,9)")
			child := roleTableRef(t, e, "c")
			inheritance := e.state.descriptors[child.Address].(*relationDescriptor).inheritance
			truncateUnitReplace(t, e, child, func(d *relationDescriptor) { d.inheritance = nil })
			waiter := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK p IN ACCESS SHARE MODE")
			stmt := truncateUnitAST("p")
			stmt.Targets[0].Only = only
			done, _ := truncateUnitAsync(t, e, waiter, stmt)
			lockPending(t, e, 1)
			truncateUnitReplace(t, e, child, func(d *relationDescriptor) { d.inheritance = inheritance })
			transactionEnd(t, holder, tx, false)
			truncateUnitJoin(t, done, "")
			want := 0
			if only {
				want = 1
			}
			if len(e.storage[p.Address].versions) != 0 || len(e.storage[child.Address].versions) != want {
				t.Fatal("grant-time hierarchy/ONLY selection wrong")
			}
			lockEmpty(t, e)
		})
	}
	t.Run("queued-same-lineage", func(t *testing.T) {
		e, holder, p, _ := truncateUnitFixture(t)
		waiter := sessionStart(t, e, nil)
		original := e.storage[p.Address]
		frontier := e.commitSeq
		tx := transactionBegin(t, holder)
		truncateUnitExecute(t, e, holder, truncateUnitAST("p"))
		done, _ := truncateUnitAsync(t, e, waiter, truncateUnitAST("p"))
		lockPending(t, e, 1)
		transactionEnd(t, holder, tx, true)
		truncateUnitJoin(t, done, "")
		if e.storage[p.Address].generation != original.generation+2 || !sameTruncateLineage(original, e.storage[p.Address]) || e.commitSeq != frontier+2 {
			t.Fatal("queued truncate was retargeted or rejected legal lineage")
		}
		lockEmpty(t, e)
	})
	t.Run("late-FK-at-grant", func(t *testing.T) {
		e, holder, p, _ := truncateUnitFixture(t)
		sessionSQL(t, holder, "CREATE TABLE c(id INT)")
		sessionSQL(t, holder, "INSERT INTO c VALUES(1)")
		child := roleTableRef(t, e, "c")
		waiter := sessionStart(t, e, nil)
		tx := transactionBegin(t, holder)
		sessionSQL(t, holder, "LOCK p IN ACCESS SHARE MODE")
		stmt := truncateUnitAST("p")
		stmt.Mode = parser.DropCascade
		done, _ := truncateUnitAsync(t, e, waiter, stmt)
		lockPending(t, e, 1)
		truncateUnitFK(t, e, p, child)
		transactionEnd(t, holder, tx, false)
		truncateUnitJoin(t, done, "")
		if len(e.storage[p.Address].versions) != 0 || len(e.storage[child.Address].versions) != 0 {
			t.Fatal("grant-time incoming graph was not captured")
		}
		lockEmpty(t, e)
	})
}
func TestTruncateAuthority(t *testing.T) {
	e, holder, p, _ := truncateUnitFixture(t)
	sessionSQL(t, holder, "CREATE USER reader")
	reader, _ := e.Roles().Lookup("reader")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.TruncatePrivilege}}}
	if err := e.SetObjectACL(context.Background(), p, acl); err != nil {
		t.Fatal(err)
	}
	waiter := sessionStart(t, e, nil)
	sessionSQL(t, waiter, "SET SESSION AUTHORIZATION reader")
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "LOCK p IN ACCESS SHARE MODE")
	original := e.storage[p.Address]
	done, _ := truncateUnitAsync(t, e, waiter, truncateUnitAST("p"))
	lockPending(t, e, 1)
	if err := e.SetObjectACL(context.Background(), p, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	transactionEnd(t, holder, tx, false)
	truncateUnitJoin(t, done, "42501")
	if e.storage[p.Address] != original || original.truncateLineage != nil {
		t.Fatal("revoked queued statement changed incarnation")
	}
	lockEmpty(t, e)
}
func TestTruncateFinalVeto(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e, s, p, q := truncateUnitFixture(t)
			var tx TxRef
			if explicit {
				tx = transactionBegin(t, s)
				sessionSQL(t, s, "INSERT INTO p VALUES(3)")
				sessionSQL(t, s, "SAVEPOINT recover")
			}
			priorWrites := s.transactions.writes[p.Address]
			oldP, oldQ := e.storage[p.Address], e.storage[q.Address]
			ctx, cancel := context.WithCancel(context.Background())
			scope, op := truncateUnitScope(t, e, s, ctx)
			if _, err := scope.Execute(truncateUnitAST("p", "q"), 0, nil); err != nil {
				t.Fatal(err)
			}
			cancel()
			if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
				t.Fatal("final cancellation not vetoed", err)
			}
			op.Done()
			if e.storage[p.Address] != oldP || e.storage[q.Address] != oldQ || oldP.truncateLineage != nil || oldQ.truncateLineage != nil {
				t.Fatal("final cancellation partially published roots")
			}
			if explicit {
				sessionSQL(t, s, "ROLLBACK TO recover")
				if !sameVersionRoot(s.transactions.writes[p.Address], priorWrites) || s.transactions.catalog != nil && len(s.transactions.catalog.storage) != 0 {
					t.Fatal("failed statement/savepoint lost prior private root")
				}
				transactionEnd(t, s, tx, false)
			}
		})
	}
}

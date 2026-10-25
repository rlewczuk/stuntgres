package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The barrier delegates real registered relation validation and pauses only its
// first call. There is no synthetic conflict or production cleanup hook. The
// ordinary route must reach this lawful outside-lock validation before preparing
// indexes; the post-drain route already has the same catalog-provider contract.
func extensionTemporaryStaleEnd(t *testing.T, mixed bool) {
	t.Helper()
	e, owner, installer, ext, closeOwner := extensionTemporaryInstalled(t)
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	if mixed {
		roleExecute(t, e, owner, `CREATE COLLATION pg_temp.ext_stale FROM pg_catalog."C"`)
		roleExecute(t, e, owner, "CREATE TABLE public.ext_collation_survivor(id INT,txt TEXT COLLATE pg_temp.ext_stale)")
		roleExecute(t, e, owner, "INSERT INTO public.ext_collation_survivor VALUES(9,'é')")
	}
	roleExecute(t, e, installer, "CREATE TEMP TABLE ext_peer_keep(id INT)")
	roleExecute(t, e, installer, "INSERT INTO ext_peer_keep VALUES(77)")
	peerNamespace := e.Catalog.State.Get(installer.Session.TemporaryNamespace)
	peerTable := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peerNamespace.Address(), Name: "ext_peer_keep"})
	peerDescriptor, peerStore := e.Catalog.State.Get(peerTable), e.Storage.Relations[peerTable]
	store := e.Storage.Relations[survivor.Address()]
	retained, retainedEnds := store.Versions, store.Ends
	index := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_survivor_id"})
	root := e.Storage.IndexRoots[index]
	held := roleExecute(t, e, installer, "SELECT txt FROM public.ext_survivor")
	defer held.Rows.Close()
	provider := e.Catalog.Providers[ids.RelationClassID]
	entered, release, joined := make(chan error, 1), make(chan struct{}), make(chan struct{})
	var fired atomic.Bool
	var unblock sync.Once
	resume := func() { unblock.Do(func() { close(release) }) }
	t.Cleanup(resume)
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
		if !fired.CompareAndSwap(false, true) {
			return
		}
		if !e.Mu.TryLock() {
			entered <- fmt.Errorf("terminal relation validation ran under Engine.mu")
			return
		}
		intact := e.Catalog.State.Get(ext.Address()) == ext && e.Catalog.State.Get(survivor.Address()) == survivor
		e.Mu.Unlock()
		if !intact {
			entered <- fmt.Errorf("candidate published before provider validation")
			return
		}
		entered <- nil
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			t.Error("terminal validation barrier exceeded5s")
		}
	}}
	go func() { closeOwner(); close(joined) }()
	callback := false
	select {
	case err := <-entered:
		callback = err == nil
		if err != nil {
			t.Error(err)
		}
	case <-joined:
		t.Error("actual session end completed without genuine terminal candidate validation")
	case <-time.After(5 * time.Second):
		t.Error("actual session end did not reach terminal validation within5s")
	}
	if callback {
		extensionTemporaryLatePeer(t, e, ext, mixed, resume)
	}
	resume()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Error("actual owner close worker did not join within5s")
		return
	}
	e.Catalog.Providers[ids.RelationClassID] = provider
	endCtx, cancelEnd := context.WithTimeout(context.Background(), 5*time.Second)
	endErr := owner.Session.WaitEnd(endCtx)
	cancelEnd()
	if endErr != nil {
		t.Error("actual stale retirement failed instead of recapturing", endErr)
	}
	if endErr == context.DeadlineExceeded {
		return
	}
	extensionTemporaryGoneUnit(t, e, ext)
	current := e.Catalog.State.MustRelation(survivor.Address())
	if current == survivor || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 || e.Storage.Relations[survivor.Address()] != store || e.Storage.IndexRoots[index] != root || !storage.SameVersionRoot(retained, store.Versions) || store.Ends != retainedEnds {
		t.Fatal("stale retirement lost surviving storage/index or failed pruning")
	}
	for _, name := range []string{"ext_view", "ext_chain", "ext_stale_late"} {
		if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: name}) != (ids.ObjectAddress{}) {
			t.Fatal("late closure retained", name)
		}
	}
	if e.Catalog.State.Get(peerNamespace.Address()) != peerNamespace || e.Catalog.State.Get(peerTable) != peerDescriptor || e.Storage.Relations[peerTable] != peerStore {
		t.Fatal("retirement changed independent peer TEMP")
	}
	if owner.Session.TemporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("dead physical owner retained namespace authority")
	}
	values := make([]sqltype.Value, 1)
	ok, err := held.Rows.Next(context.Background(), values)
	text, textErr := values[0].Text()
	if err != nil || textErr != nil || !ok || text != "Hotel" {
		t.Fatal("retained captured row changed", ok, text, err, textErr)
	}
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id) VALUES(2)")
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id,txt) VALUES(3,'Hôtel')")
	extensionRetirementRowsUnit(t, e, installer)
	if callback {
		durable := roleExecute(t, e, installer, "SELECT id FROM public.ext_peer_durable")
		value := make([]sqltype.Value, 1)
		next, err := durable.Rows.Next(context.Background(), value)
		id, valueErr := value[0].Integer()
		closeErr := durable.Rows.Close()
		if !next || err != nil || valueErr != nil || closeErr != nil || id != 41 {
			t.Fatal("peer durable publication lost", next, id, err, valueErr, closeErr)
		}
	}
	peerRows := roleExecute(t, e, installer, "SELECT id FROM ext_peer_keep")
	peerValue := make([]sqltype.Value, 1)
	peerNext, peerErr := peerRows.Rows.Next(context.Background(), peerValue)
	peerID, peerValueErr := peerValue[0].Integer()
	closeErr := peerRows.Rows.Close()
	if !peerNext || peerErr != nil || peerValueErr != nil || closeErr != nil || peerID != 77 {
		t.Fatal("peer TEMP row changed", peerID, peerErr, peerValueErr, closeErr)
	}
	if mixed {
		collated := e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_collation_survivor"}))
		if len(collated.Columns) != 1 || collated.Columns[0].Name != "id" || len(e.Storage.Relations[collated.Address()].Versions) != 1 {
			t.Fatal("mixed collation consumer not projected")
		}
	}
}

// Ordinary preparation has no retirement ticket yet, so a peer view can publish
// and force whole-root recapture. Post-drain maintenance already holds the
// procedure's AX lock: publish an unrelated durable root first, then prove the
// checked view waits for retirement rather than creating an impossible
// synchronous late-consumer schedule. Disjoint durable publication is preserved
// by the maintenance merge; it does not itself require a new closure.
func extensionTemporaryLatePeer(t *testing.T, e *Engine, ext *catalog.Extension, mixed bool, resume func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peer, err := e.StartSession(ctx, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: e.RoleStore.Domain.Principal(10)})
	if err != nil {
		t.Fatal("bounded independent peer start", err)
	}
	defer func() {
		peer.RequestEnd(session.CleanClose)
		endCtx, cancelEnd := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelEnd()
		if err := peer.WaitEnd(endCtx); err != nil {
			t.Error("bounded independent peer end", err)
		}
	}()
	op, err := peer.Acquire(ctx)
	if err != nil {
		t.Fatal("bounded independent peer admission", err)
	}
	defer op.Done()
	read := e.CatalogRead()
	namespace := read.Get(ext.TargetNamespace).Name()
	stmt := statement(t, "CREATE VIEW public.ext_stale_late AS SELECT "+namespace+".unaccent('Hôtel'::text) AS v")
	durable := func() {
		for _, query := range []string{"CREATE TABLE public.ext_peer_durable(id INT)", "INSERT INTO public.ext_peer_durable VALUES(41)"} {
			if err := extensionUnitExecute(t, e, op, statement(t, query)); err != nil {
				t.Fatal("peer durable publication", err)
			}
		}
	}
	if !mixed {
		if err := extensionUnitExecute(t, e, op, stmt); err != nil {
			t.Fatal("peer late consumer publication", err)
		}
		durable()
		return
	}
	durable()
	var target ids.ObjectAddress
	for _, address := range ext.Members {
		if p, ok := read.Procedure(address); ok && p.Name() == "unaccent" && p.Signature == catalog.UnaccentTextSignature {
			target = address
			break
		}
	}
	if target == (ids.ObjectAddress{}) {
		t.Fatal("missing genuine temporary unaccent(text) provider")
	}
	result := make(chan error, 1)
	joined := false
	defer func() {
		resume()
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("late temporary view worker did not join")
			}
		}
	}()
	go func() { result <- extensionUnitExecute(t, e, op, stmt) }()
	lockPending(t, e, 1)
	entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: read.Domain().Ref(read.Epoch(), target)}]
	maintenanceAX := false
	if entry != nil {
		for ticket, hold := range entry.Holders {
			if hold.Mode&lock.AccessExclusive != 0 && session.PrivateSessionOf(session.TxOwnerOf(ticket.Ref()).Session.SessionToken) != nil {
				maintenanceAX = true
			}
		}
	}
	if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != lock.AccessShare || session.TxOwnerOf(entry.Pending[0].Ticket.Ref()).Session != peer || !maintenanceAX {
		t.Fatal("late view did not wait on the exact temporary procedure's maintenance AX lock", target)
	}
	resume()
	select {
	case err = <-result:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("late temporary view worker did not finish after retirement")
	}
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "referenced function was concurrently dropped"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("retired temporary view provider: got %v, want %#v", err, want)
	}
}

func extensionTemporarySaturatedEnd(t *testing.T) {
	t.Helper()
	e, owner, installer, ext, closeOwner := extensionTemporaryInstalled(t)
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	roleExecute(t, e, owner, `CREATE COLLATION pg_temp.ext_maximum FROM pg_catalog."C"`)
	roleExecute(t, e, installer, "CREATE TEMP TABLE ext_max_peer(id INT)")
	roleExecute(t, e, installer, "INSERT INTO ext_max_peer VALUES(77)")
	peerNamespace := e.Catalog.State.Get(installer.Session.TemporaryNamespace)
	peerTable := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peerNamespace.Address(), Name: "ext_max_peer"})
	peerDescriptor, peerStore := e.Catalog.State.Get(peerTable), e.Storage.Relations[peerTable]
	store := e.Storage.Relations[survivor.Address()]
	retained, ends := store.Versions, store.Ends
	index := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_survivor_id"})
	root := e.Storage.IndexRoots[index]
	maximum := *survivor
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(survivor.Address(), &maximum)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	joined := make(chan struct{})
	go func() { closeOwner(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("saturated mixed owner close worker did not join within5s")
	}
	endCtx, cancelEnd := context.WithTimeout(context.Background(), 5*time.Second)
	endErr := owner.Session.WaitEnd(endCtx)
	cancelEnd()
	if endErr != nil {
		t.Error("mixed terminal saturation failed", endErr)
	}
	if endErr == context.DeadlineExceeded {
		return
	}
	extensionTemporaryGoneUnit(t, e, ext)
	current := e.Catalog.State.MustRelation(survivor.Address())
	if current == &maximum || current.Revision() != maximum.Revision() || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 || len(maximum.ColumnMeta.Defaults) != 1 || len(maximum.Constraints.Check) != 1 || e.Catalog.NextRevision() != maximum.Revision() {
		t.Fatal("post-drain did not preserve terminal counter/immutable predecessor contract")
	}
	if e.Storage.Relations[survivor.Address()] != store || e.Storage.IndexRoots[index] != root || !storage.SameVersionRoot(retained, store.Versions) || ends != store.Ends || e.Catalog.State.Get(peerNamespace.Address()) != peerNamespace || e.Catalog.State.Get(peerTable) != peerDescriptor || e.Storage.Relations[peerTable] != peerStore {
		t.Fatal("terminal mixed cleanup changed storage, index, old rows or peer TEMP")
	}
	for _, name := range []string{"ext_view", "ext_chain"} {
		if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: name}) != (ids.ObjectAddress{}) {
			t.Fatal("saturated view closure retained", name)
		}
	}
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id) VALUES(2)")
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id,txt) VALUES(3,'Hôtel')")
	extensionRetirementRowsUnit(t, e, installer)
	if len(retained) != 1 || len(e.Storage.Relations[survivor.Address()].Versions) != 3 {
		t.Fatal("retained versus current row publication")
	}
}

func extensionRetirementRowsUnit(t *testing.T, e *Engine, op *session.Operation) {
	t.Helper()
	result := roleExecute(t, e, op, "SELECT id,txt FROM public.ext_survivor ORDER BY id")
	defer result.Rows.Close()
	values := make([]sqltype.Value, 2)
	for i, expected := range []string{"Hotel", "", "Hôtel"} {
		next, err := result.Rows.Next(context.Background(), values)
		id, idErr := values[0].Integer()
		if !next || err != nil || idErr != nil || id != int64(i+1) {
			t.Fatal("surviving row identity", next, id, err, idErr)
		}
		if i == 1 {
			if !values[1].IsNull() {
				t.Fatal("retired DEFAULT still supplied a value")
			}
		} else {
			text, err := values[1].Text()
			if err != nil || text != expected {
				t.Fatal("surviving row value or CHECK retirement", text, expected, err)
			}
		}
	}
	if next, err := result.Rows.Next(context.Background(), values); next || err != nil {
		t.Fatal("extra surviving row", next, err)
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func extensionTemporaryStructuralEnd(t *testing.T, mixed bool) {
	t.Helper()
	e := New()
	ownerSession, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: e.RoleStore.Domain.Principal(10)})
	if err != nil {
		t.Fatal(err)
	}
	var owner *session.Operation
	expectedFailure := false
	t.Cleanup(func() {
		if owner != nil {
			owner.Done()
		}
		ownerSession.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := ownerSession.WaitEnd(ctx)
		if !expectedFailure && err != nil {
			t.Error("setup owner close", err)
		}
		if expectedFailure && (err == nil || !strings.Contains(err.Error(), "invalid relation column identity")) {
			t.Error("structural failure was lost", err)
		}
	})
	owner, err = ownerSession.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	installer, _ := extensionTemporaryOperation(t, e, "postgres")
	roleExecute(t, e, owner, "CREATE TEMP TABLE ext_structural_seed(id INT)")
	namespace := e.Catalog.State.Get(ownerSession.TemporaryNamespace).Name()
	roleExecute(t, e, installer, "CREATE EXTENSION unaccent SCHEMA "+namespace)
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	if mixed {
		roleExecute(t, e, owner, `CREATE COLLATION pg_temp.ext_structural FROM pg_catalog."C"`)
	}
	// A genuinely malformed immutable survivor reaches the real relation
	// provider. No callback returns a fabricated error or advances the root.
	broken := *survivor
	broken.Index = maps.Clone(survivor.Index)
	broken.Index["txt"] = len(broken.Columns)
	e.Mu.Lock()
	root := catalog.CloneState(e.Catalog.State)
	root.SetDescriptor(broken.Address(), &broken)
	e.Catalog.PublishLocked(root)
	e.Mu.Unlock()
	expectedFailure = true
	owner.Done()
	ownerSession.RequestEnd(session.CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = ownerSession.WaitEnd(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid relation column identity") {
		t.Fatal("actual unchanged structural failure must escape promptly", err)
	}
	if e.Catalog.State != root || e.Catalog.State.Get(ext.Address()) != ext || e.Catalog.State.Get(survivor.Address()) != &broken {
		t.Fatal("structural error published partial retirement")
	}
}

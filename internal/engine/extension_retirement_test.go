package engine

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	peerNamespace := e.catalog.state.Get(installer.session.temporaryNamespace)
	peerTable := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peerNamespace.Address(), Name: "ext_peer_keep"})
	peerDescriptor, peerStore := e.catalog.state.Get(peerTable), e.storage.Relations[peerTable]
	store := e.storage.Relations[survivor.Address()]
	retained, retainedEnds := store.Versions, store.Ends
	index := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_survivor_id"})
	root := e.storage.IndexRoots[index]
	held := roleExecute(t, e, installer, "SELECT txt FROM public.ext_survivor")
	defer held.Rows.Close()
	provider := e.catalog.Providers[ids.RelationClassID]
	entered, release, joined := make(chan error, 1), make(chan struct{}), make(chan struct{})
	var fired atomic.Bool
	var unblock sync.Once
	resume := func() { unblock.Do(func() { close(release) }) }
	t.Cleanup(resume)
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
		if !fired.CompareAndSwap(false, true) {
			return
		}
		if !e.mu.TryLock() {
			entered <- fmt.Errorf("terminal relation validation ran under Engine.mu")
			return
		}
		intact := e.catalog.state.Get(ext.Address()) == ext && e.catalog.state.Get(survivor.Address()) == survivor
		e.mu.Unlock()
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
		// A new independent peer consumer does not borrow a held survivor/view
		// relation lock: it binds the still-committed genuine procedure directly.
		namespace := e.catalog.state.Get(ext.TargetNamespace).Name()
		stmt := statement(t, "CREATE VIEW public.ext_stale_late AS SELECT "+namespace+".unaccent('Hôtel'::text) AS v")
		peerCtx, cancelPeer := context.WithTimeout(context.Background(), 5*time.Second)
		peer, err := e.StartSession(peerCtx, SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: e.authorizationDomain.Principal(10)})
		if err != nil {
			t.Error("bounded independent peer start", err)
		} else {
			peerOp, err := peer.Acquire(peerCtx)
			if err != nil {
				t.Error("bounded independent peer admission", err)
			} else {
				if err := extensionUnitExecute(t, e, peerOp, stmt); err != nil {
					t.Error("peer late consumer publication", err)
				}
				for _, query := range []string{"CREATE TABLE public.ext_peer_durable(id INT)", "INSERT INTO public.ext_peer_durable VALUES(41)"} {
					if err := extensionUnitExecute(t, e, peerOp, statement(t, query)); err != nil {
						t.Error("peer durable publication", err)
						break
					}
				}
				peerOp.Done()
			}
			peer.RequestEnd(CleanClose)
			peerEndCtx, cancelPeerEnd := context.WithTimeout(context.Background(), 5*time.Second)
			if err := peer.WaitEnd(peerEndCtx); err != nil {
				t.Error("bounded independent peer end", err)
			}
			cancelPeerEnd()
		}
		cancelPeer()
	}
	resume()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Error("actual owner close worker did not join within5s")
		return
	}
	e.catalog.Providers[ids.RelationClassID] = provider
	endCtx, cancelEnd := context.WithTimeout(context.Background(), 5*time.Second)
	endErr := owner.session.WaitEnd(endCtx)
	cancelEnd()
	if endErr != nil {
		t.Error("actual stale retirement failed instead of recapturing", endErr)
	}
	if endErr == context.DeadlineExceeded {
		return
	}
	extensionTemporaryGoneUnit(t, e, ext)
	current := e.catalog.state.MustRelation(survivor.Address())
	if current == survivor || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 || e.storage.Relations[survivor.Address()] != store || e.storage.IndexRoots[index] != root || !sameVersionRoot(retained, store.Versions) || store.Ends != retainedEnds {
		t.Fatal("stale retirement lost surviving storage/index or failed pruning")
	}
	for _, name := range []string{"ext_view", "ext_chain", "ext_stale_late"} {
		if e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: name}) != (ids.ObjectAddress{}) {
			t.Fatal("late closure retained", name)
		}
	}
	if e.catalog.state.Get(peerNamespace.Address()) != peerNamespace || e.catalog.state.Get(peerTable) != peerDescriptor || e.storage.Relations[peerTable] != peerStore {
		t.Fatal("retirement changed independent peer TEMP")
	}
	if owner.session.temporaryNamespace != (ids.ObjectAddress{}) {
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
		collated := e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_collation_survivor"}))
		if len(collated.Columns) != 1 || collated.Columns[0].Name != "id" || len(e.storage.Relations[collated.Address()].Versions) != 1 {
			t.Fatal("mixed collation consumer not projected")
		}
	}
}

func extensionTemporarySaturatedEnd(t *testing.T) {
	t.Helper()
	e, owner, installer, ext, closeOwner := extensionTemporaryInstalled(t)
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	roleExecute(t, e, owner, `CREATE COLLATION pg_temp.ext_maximum FROM pg_catalog."C"`)
	roleExecute(t, e, installer, "CREATE TEMP TABLE ext_max_peer(id INT)")
	roleExecute(t, e, installer, "INSERT INTO ext_max_peer VALUES(77)")
	peerNamespace := e.catalog.state.Get(installer.session.temporaryNamespace)
	peerTable := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peerNamespace.Address(), Name: "ext_max_peer"})
	peerDescriptor, peerStore := e.catalog.state.Get(peerTable), e.storage.Relations[peerTable]
	store := e.storage.Relations[survivor.Address()]
	retained, ends := store.Versions, store.Ends
	index := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_survivor_id"})
	root := e.storage.IndexRoots[index]
	maximum := *survivor
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	e.mu.Lock()
	next := catalog.CloneState(e.catalog.state)
	next.SetDescriptor(survivor.Address(), &maximum)
	e.catalog.state = next
	e.mu.Unlock()
	joined := make(chan struct{})
	go func() { closeOwner(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("saturated mixed owner close worker did not join within5s")
	}
	endCtx, cancelEnd := context.WithTimeout(context.Background(), 5*time.Second)
	endErr := owner.session.WaitEnd(endCtx)
	cancelEnd()
	if endErr != nil {
		t.Error("mixed terminal saturation failed", endErr)
	}
	if endErr == context.DeadlineExceeded {
		return
	}
	extensionTemporaryGoneUnit(t, e, ext)
	current := e.catalog.state.MustRelation(survivor.Address())
	if current == &maximum || current.Revision() != maximum.Revision() || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 || len(maximum.ColumnMeta.Defaults) != 1 || len(maximum.Constraints.Check) != 1 || e.catalog.nextRevision != maximum.Revision() {
		t.Fatal("post-drain did not preserve terminal counter/immutable predecessor contract")
	}
	if e.storage.Relations[survivor.Address()] != store || e.storage.IndexRoots[index] != root || !sameVersionRoot(retained, store.Versions) || ends != store.Ends || e.catalog.state.Get(peerNamespace.Address()) != peerNamespace || e.catalog.state.Get(peerTable) != peerDescriptor || e.storage.Relations[peerTable] != peerStore {
		t.Fatal("terminal mixed cleanup changed storage, index, old rows or peer TEMP")
	}
	for _, name := range []string{"ext_view", "ext_chain"} {
		if e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: name}) != (ids.ObjectAddress{}) {
			t.Fatal("saturated view closure retained", name)
		}
	}
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id) VALUES(2)")
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id,txt) VALUES(3,'Hôtel')")
	extensionRetirementRowsUnit(t, e, installer)
	if len(retained) != 1 || len(e.storage.Relations[survivor.Address()].Versions) != 3 {
		t.Fatal("retained versus current row publication")
	}
}

func extensionRetirementRowsUnit(t *testing.T, e *Engine, op *Operation) {
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
	ownerSession, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: e.authorizationDomain.Principal(10)})
	if err != nil {
		t.Fatal(err)
	}
	var owner *Operation
	expectedFailure := false
	t.Cleanup(func() {
		if owner != nil {
			owner.Done()
		}
		ownerSession.RequestEnd(CleanClose)
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
	namespace := e.catalog.state.Get(ownerSession.temporaryNamespace).Name()
	roleExecute(t, e, installer, "CREATE EXTENSION unaccent SCHEMA "+namespace)
	ext := e.catalog.state.MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	if mixed {
		roleExecute(t, e, owner, `CREATE COLLATION pg_temp.ext_structural FROM pg_catalog."C"`)
	}
	// A genuinely malformed immutable survivor reaches the real relation
	// provider. No callback returns a fabricated error or advances the root.
	broken := *survivor
	broken.Index = maps.Clone(survivor.Index)
	broken.Index["txt"] = len(broken.Columns)
	e.mu.Lock()
	root := catalog.CloneState(e.catalog.state)
	root.SetDescriptor(broken.Address(), &broken)
	e.catalog.state = root
	e.mu.Unlock()
	expectedFailure = true
	owner.Done()
	ownerSession.RequestEnd(CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = ownerSession.WaitEnd(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid relation column identity") {
		t.Fatal("actual unchanged structural failure must escape promptly", err)
	}
	if e.catalog.state != root || e.catalog.state.Get(ext.Address()) != ext || e.catalog.state.Get(survivor.Address()) != &broken {
		t.Fatal("structural error published partial retirement")
	}
}

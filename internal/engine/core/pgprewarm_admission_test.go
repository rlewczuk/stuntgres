package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type prewarmBorrowedView struct {
	view *visibility.View
	data *visibility.Data
	local bool
}

// Every observation follows a real delegate. Retained immutable data and actual
// view handles distinguish resource demand and release from host call counts.
type prewarmAdmissionHost struct {
	*prewarmCountingHost
	views []prewarmBorrowedView
	catalogPayloads []int64
	generators []*storage.SequenceGenerator
	allocations []storage.Allocation
	afterRelease func()
	afterCatalog func()
	released bool
}

func (h *prewarmAdmissionHost) RelationDataViewLocked(ref ids.ObjectRef) (*visibility.View, bool, error) {
	view, local, err := h.Host.RelationDataViewLocked(ref)
	if err == nil && view != nil { h.views = append(h.views, prewarmBorrowedView{view, view.Load(), local}) }
	return view, local, err
}

func (h *prewarmAdmissionHost) PgvisibilityCatalogBytes(ref ids.ObjectRef, target *catalog.Relation) (int64, error) {
	payload, err := h.Host.PgvisibilityCatalogBytes(ref, target)
	if err == nil {
		h.catalogPayloads = append(h.catalogPayloads, payload)
		if h.afterCatalog != nil { h.afterCatalog() }
	}
	return payload, err
}

func (h *prewarmAdmissionHost) SequenceGeneratorLocked(address ids.ObjectAddress) *storage.SequenceGenerator {
	generator := h.Host.SequenceGeneratorLocked(address)
	h.generators = append(h.generators, generator)
	return generator
}

func (h *prewarmAdmissionHost) RelationAllocationLocked(read catalog.Read, target *catalog.Relation) storage.Allocation {
	allocation := h.Host.RelationAllocationLocked(read, target)
	h.allocations = append(h.allocations, allocation)
	return allocation
}

func (h *prewarmAdmissionHost) ReleaseOwnerEngineLocked() {
	h.Host.ReleaseOwnerEngineLocked()
	if !h.released {
		h.released = true
		if h.afterRelease != nil { h.afterRelease() }
	}
}

func prewarmUnlocked(t *testing.T, e *core.Engine, scope *core.StatementScope) {
	t.Helper()
	if !scope.Owner.Mu.TryLock() { t.Fatal("capture retained transaction-owner lock") }
	scope.Owner.Mu.Unlock()
	if !e.Mu.TryLock() { t.Fatal("capture retained Engine lock") }
	e.Mu.Unlock()
}

func prewarmAdmissionCache(t *testing.T, e *core.Engine, observer *session.Session) map[int64]sqltype.Value {
	t.Helper()
	cursor := bufferUnitCursor(t, e, observer)
	defer cursor.Close()
	rows := make(map[int64]sqltype.Value)
	for {
		row, present, err := cursor.Next(t.Context())
		if err != nil { t.Fatal(err) }
		if !present { return rows }
		if row.CompositeField(1).IsNull() { continue }
		slot, err := row.CompositeField(0).Integer()
		if err != nil { t.Fatal(err) }
		pins, err := row.CompositeField(8).Integer()
		if err != nil || pins != 0 { t.Fatal("unreleased cache pin", pins, err) }
		if _, duplicate := rows[slot]; duplicate { t.Fatal("duplicate real cache slot", slot) }
		rows[slot] = row
	}
}

func prewarmAdmissionLock(h *prewarmAdmissionHost, ref ids.ObjectRef) *lock.ResourceState {
	return h.LockManager().Inspect(h.Ticket()).Resources[lock.Resource{ObjectRef: ref}]
}

func prewarmAdmissionReleased(t *testing.T, h *prewarmAdmissionHost, ref ids.ObjectRef, before *lock.ResourceState) {
	t.Helper()
	if after := prewarmAdmissionLock(h, ref); !reflect.DeepEqual(before, after) { t.Fatal("inspection leaked or changed relation contribution ownership", before, after) }
	for _, borrowed := range h.views {
		if borrowed.data == nil || borrowed.data.Catalog.Owner() == nil { t.Fatal("observation did not retain real captured roots") }
		if borrowed.local && borrowed.view.Load() != nil { t.Fatal("local sizing view outlived invocation") }
	}
}

func prewarmAdmissionDiagnostic(t *testing.T, count int64, err error, code, message string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	var got *sqlstate.Error
	if count != 0 || !errors.As(err, &got) || !reflect.DeepEqual(*got, want) { t.Fatal("complete independently expected body diagnostic", count, got, err, want) }
}

func TestPgPrewarmStorageAdmission(t *testing.T) {
	t.Run("actual_catalog_producer", func(t *testing.T) {
		e, _, n := prewarmOwnerEngine(t)
		actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		read := e.CatalogRead()
		ref := enginetest.RoleTableRef(t, e, "pg_catalog.pg_am")
		target := read.MustRelation(ref.Address)
		if ref.Address.ObjectID != 2601 || target.RelationKind != catalog.CatalogRelation || target.CatalogView || target.EffectiveStorageLocator(read) != 0 || e.Storage.Relations[ref.Address] != nil { t.Fatal("not a genuine cache-ineligible catalog producer", target) }
		before := prewarmAdmissionCache(t, e, observer)
		for _, request := range []storage.PrewarmRequest{
			{DefaultFirst: true, DefaultLast: true},
			{First: 0, Last: 0},
			{Fork: 1, First: -1, Last: -1},
			{Fork: 2, First: -1, Last: -1},
			{Fork: 3, First: -1, Last: -1},
			{First: 1, Last: -1},
		} {
			scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
			host := &prewarmAdmissionHost{prewarmCountingHost: &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView])}}
			baseline := prewarmAdmissionLock(host, ref)
			actor.Mu.Lock(); resources := actor.Resources; actor.Mu.Unlock()
			count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), request)
			switch request.Fork {
			case 1, 2, 3:
				prewarmAdmissionDiagnostic(t, count, err, "22023", fmt.Sprintf("fork %q does not exist for this relation", []string{"main", "fsm", "vm", "init"}[request.Fork]))
			default:
				if request.First == 1 { prewarmAdmissionDiagnostic(t, count, err, "22023", "starting block number must be between 0 and 0") } else if err != nil || count != 1 || host.completed != 1 { t.Fatal("real producer main count/admission", count, host.completed, err) }
			}
			if request.Fork == 0 && !reflect.DeepEqual(host.catalogPayloads, []int64{868}) { t.Fatal("seven real catalog rows did not supply independent 868-byte payload", host.catalogPayloads) }
			if len(host.views) != 0 { t.Fatal("catalog producer acquired heap sizing storage") }
			prewarmAdmissionReleased(t, host, ref, baseline)
			actor.Mu.Lock(); unchanged := actor.Resources == resources; actor.Mu.Unlock()
			if !unchanged { t.Fatal("catalog producer leaked session resource attachment") }
			finish(err)
			if !reflect.DeepEqual(before, prewarmAdmissionCache(t, e, observer)) { t.Fatal("catalog range exported native shared occupancy") }
		}
	})

	for _, action := range []string{"descriptor", "cancel"} {
		t.Run("catalog_after_producer_"+action, func(t *testing.T) {
			e, _, n := prewarmOwnerEngine(t)
			actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			ref := enginetest.RoleTableRef(t, e, "pg_catalog.pg_am")
			before := prewarmAdmissionCache(t, e, observer)
			ctx, cancel := context.WithCancel(t.Context()); defer cancel()
			scope, finish := prewarmOwnerScope(t, ctx, e, actor)
			host := &prewarmAdmissionHost{prewarmCountingHost: &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView])}}
			baseline := prewarmAdmissionLock(host, ref)
			host.afterCatalog = func() {
				prewarmUnlocked(t, e, scope)
				if action == "cancel" { cancel() } else { enginetest.Run(t, e, "REVOKE SELECT ON pg_catalog.pg_am FROM PUBLIC") }
				before = prewarmAdmissionCache(t, e, observer)
			}
			count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{DefaultFirst: true, DefaultLast: true})
			want := ids.ErrCatalogReplaced
			if action == "cancel" { want = context.Canceled }
			if count != 0 || host.completed != 0 || !errors.Is(err, want) || !reflect.DeepEqual(host.catalogPayloads, []int64{868}) { t.Fatal("actual producer completion did not fence stale/canceled admission", count, host.completed, host.catalogPayloads, err) }
			prewarmAdmissionReleased(t, host, ref, baseline)
			finish(err)
			if !reflect.DeepEqual(before, prewarmAdmissionCache(t, e, observer)) { t.Fatal("failed catalog admission changed cache") }
			enginetest.TransactionRows(t, actor, "SELECT pg_prewarm(2601::oid::regclass)", 1)
		})
	}

	for _, action := range []string{"success", "descriptor", "rows", "ends", "auxiliary", "cancel"} {
		t.Run("inline_text_"+action, func(t *testing.T) {
			e, _, n := prewarmOwnerEngine(t)
			enginetest.Run(t, e, "CREATE TABLE prewarm_inline(payload text)")
			enginetest.Run(t, e, "INSERT INTO prewarm_inline SELECT repeat('inline',4000) FROM generate_series(1,121)")
			actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			read := e.CatalogRead()
			ref := enginetest.CatalogRef(t, e, read, "prewarm_inline")
			target, store := read.MustRelation(ref.Address), e.Storage.Relations[ref.Address]
			versions, ends, headers, generation, auxiliary := store.Versions, store.Ends, store.Headers, store.Generation, store.Auxiliary
			retainedRows := slices.Clone(versions)
			if target.RelationKind != catalog.TableRelation || target.EffectiveStorageLocator(read) == 0 || len(versions) != 121 { t.Fatal("not genuine native inline storage") }
			expectedPayload := strings.Repeat("inline", 4000)
			for _, row := range versions {
				payload, err := row.Values[0].Text()
				if err != nil || payload != expectedPayload { t.Fatal("lost genuine inline payload", err) }
			}
			before := prewarmAdmissionCache(t, e, observer)
			ctx, cancel := context.WithCancel(t.Context()); defer cancel()
			scope, finish := prewarmOwnerScope(t, ctx, e, actor)
			host := &prewarmAdmissionHost{prewarmCountingHost: &prewarmCountingHost{Host: core.HostOf(scope).(contrib.Host[core.AuthorizationView])}}
			baseline := prewarmAdmissionLock(host, ref)
			host.afterRelease = func() {
				prewarmUnlocked(t, e, scope)
				switch action {
				case "descriptor": enginetest.Run(t, e, "GRANT SELECT ON prewarm_inline TO PUBLIC")
				case "rows": enginetest.Run(t, e, "INSERT INTO prewarm_inline VALUES('later')")
				case "ends": enginetest.Run(t, e, "DELETE FROM prewarm_inline")
				case "auxiliary": enginetest.Run(t, e, "VACUUM (FREEZE) prewarm_inline")
				case "cancel": cancel()
				}
				// Ordinary mutator reads retain their own legitimate cache
				// effects. Only subsequent stale-prewarm work must be absent.
				before = prewarmAdmissionCache(t, e, observer)
			}
			count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{DefaultFirst: true, DefaultLast: true})
			if action == "success" {
				if err != nil || count != 2 || host.completed != 2 { t.Fatal("121 independent 68-byte rows must process two blocks", count, host.completed, err) }
				if e.CatalogRead().MustRelation(ref.Address) != target || e.Storage.Relations[ref.Address] != store || !storage.SameVersionRoot(versions, store.Versions) || store.Ends != ends || store.Headers != headers || store.Generation != generation || store.Auxiliary != auxiliary { t.Fatal("prewarm changed actual accepted inline backing") }
			} else {
				want := ids.ErrCatalogReplaced
				if action == "cancel" { want = context.Canceled }
				if count != 0 || host.completed != 0 || !errors.Is(err, want) { t.Fatal("inline storage capture did not reject real successor/cancel", count, host.completed, err) }
			}
			if len(host.views) != 1 || host.views[0].data.GenerationAt(ref.Address) != store || host.views[0].data.Catalog.Get(ref.Address) != target { t.Fatal("did not observe actual captured inline storage/definition") }
			prewarmAdmissionReleased(t, host, ref, baseline)
			finish(err)
			for _, borrowed := range host.views { if borrowed.view.Load() != nil { t.Fatal("inline sizing view escaped finished owner") } }
			if !reflect.DeepEqual(versions, retainedRows) { t.Fatal("genuine successor mutated retained inline prefix") }
			if action == "success" {
				want := map[[2]int64][2]int64{{0,0}: {1,0}, {0,1}: {1,0}}
				if got := prewarmOwnerCoordinates(t, e, observer, target.EffectiveStorageLocator(read)); !reflect.DeepEqual(got, want) { t.Fatal("inline storage did not load exact eligible coordinates with released pins", got) }
			} else if !reflect.DeepEqual(before, prewarmAdmissionCache(t, e, observer)) { t.Fatal("rejected inline capture changed cache") }
			enginetest.TransactionRows(t, actor, "SELECT reltoastrelid::bigint FROM pg_class WHERE oid='prewarm_inline'::regclass", 0)
			enginetest.TransactionRows(t, actor, "SELECT 1", 1)
		})
	}
}

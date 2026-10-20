package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func domainAlterPublicationFixture(t *testing.T) (*Engine, *Operation, func(), *catalog.Type, *catalog.Relation, *CatalogStage, *PreparedCatalogPublication) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.d AS integer CONSTRAINT positive CHECK(VALUE>0)"))
	table := domainUnitTable(t, e, op, "CREATE TABLE public.stored(v public.d)")
	view := e.CatalogRead()
	d := domainUnitType(t, view, "d")
	invalid := *d
	invalid.DomainChecks = slices.Clone(d.DomainChecks)
	invalid.DomainChecks[0].NotValid = true
	invalid.ObjectRevision++
	setup := e.BeginCatalogStage(view)
	if err := setup.Replace(&invalid); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, setup)
	view = e.CatalogRead()
	d = domainUnitType(t, view, "d")
	physical, err := sqltype.Integer(d.SQLType.BaseType(), 1)
	if err != nil {
		t.Fatal(err)
	}
	value, err := d.SQLType.Wrap(physical)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.storage.InstallRelationLocked(table.Address(), &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}}}})
	e.mu.Unlock()
	e.visibility.SetCommitSeqLocked(1)
	stage := e.BeginCatalogStage(view)
	next := *d
	next.DomainChecks = slices.Clone(d.DomainChecks)
	next.DomainChecks[0].NotValid = false
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	stage.domainBases = []catalog.Descriptor{d}
	stage.domainValidation = domainAlterUnitFence(t, e, view, d.Address())
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	return e, op, done, d, table, stage, prepared
}

func TestDomainAlterConstraintPublication(t *testing.T) {
	for _, kind := range []string{"success", "unrelated", "late_use", "stale_domain", "owner", "root", "length", "generation", "ends", "replacement", "provider"} {
		t.Run(kind, func(t *testing.T) {
			e, op, done, d, table, stage, prepared := domainAlterPublicationFixture(t)
			defer done()
			defer e.DiscardCatalogStage(stage)
			switch kind {
			case "unrelated":
				domainUnitTable(t, e, op, "CREATE TABLE public.unrelated(v integer)")
			case "late_use":
				domainUnitTable(t, e, op, "CREATE TABLE public.late(v public.d)")
			case "stale_domain", "owner":
				state := catalog.CloneState(e.catalog.state)
				changed := *d
				changed.ObjectRevision++
				if kind == "owner" {
					aclUnitRoles(e, "domain_alter_owner")
					changed.Owner = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("domain_alter_owner")
				}
				state.SetDescriptor(d.Address(), &changed)
				e.catalog.state = state
			case "root", "length", "generation", "ends":
				store := *e.storage.Relations[table.Address()]
				switch kind {
				case "length":
					store.Versions = append(slices.Clone(store.Versions), storage.RowVersion{Row: 2, Version: 2})
				case "generation":
					store.Generation++
				case "ends":
					store.Ends = &storage.DeletionRoot{}
				}
				e.mu.Lock()
				e.storage.InstallRelationLocked(table.Address(), &store)
				e.mu.Unlock()
			case "replacement":
				state := catalog.CloneState(e.catalog.state)
				state.SetEpoch(state.Epoch() + 1)
				e.catalog.state = state
			case "provider":
				stage.domainBases = append(stage.domainBases, e.catalog.state.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}))
				state := catalog.CloneState(e.catalog.state)
				provider := *state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
				provider.ObjectRevision++
				state.SetDescriptor(provider.Address(), &provider)
				e.catalog.state = state
			}
			before := e.catalog.state
			stores := maps.Clone(e.storage.Relations)
			roots := maps.Clone(e.storage.IndexRoots)
			_, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if kind == "success" || kind == "unrelated" {
				if err != nil || domainUnitType(t, e.CatalogRead(), "d").DomainChecks[0].NotValid {
					t.Fatal("validity not atomically installed", err)
				}
			} else {
				if err == nil || e.catalog.state != before || !maps.Equal(e.storage.Relations, stores) || !maps.Equal(e.storage.IndexRoots, roots) || !d.DomainChecks[0].NotValid {
					t.Fatal("veto published partial roots", err)
				}
			}
		})
	}
	for _, mode := range []string{"cancel_before", "cancel_after", "success"} {
		t.Run(mode, func(t *testing.T) {
			e, _, done, d, table, stage, prepared := domainAlterPublicationFixture(t)
			defer done()
			defer e.DiscardCatalogStage(stage)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			barrier := &transactionBarrierContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
			barrier.checks.Store(1)
			if mode == "cancel_after" {
				barrier.late = cancel
			}
			prepared.ctx = barrier
			before, store := e.catalog.state, e.storage.Relations[table.Address()]
			finished := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				select {
				case <-barrier.release:
				default:
					close(barrier.release)
				}
				if !joined {
					select {
					case <-finished:
					case <-time.After(5 * time.Second):
						t.Error("publication worker did not join")
					}
				}
			}()
			go func() {
				_, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
				finished <- err
			}()
			select {
			case <-barrier.entered:
			case err := <-finished:
				joined = true
				t.Fatal("missed real final barrier", err)
			case <-time.After(5 * time.Second):
				t.Fatal("final barrier deadline")
			}
			if mode == "cancel_before" {
				cancel()
			}
			close(barrier.release)
			var err error
			select {
			case err = <-finished:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("publication completion deadline")
			}
			if mode == "cancel_before" {
				if !errors.Is(err, context.Canceled) || e.catalog.state != before || e.storage.Relations[table.Address()] != store || !d.DomainChecks[0].NotValid {
					t.Fatal("cancellation partially installed", err)
				}
			} else if err != nil || e.catalog.state == before || domainUnitType(t, e.CatalogRead(), "d").DomainChecks[0].NotValid || !d.DomainChecks[0].NotValid {
				t.Fatal("successful install retracted", err)
			}
		})
	}
}

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
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func domainAlterPublicationFixture(t *testing.T) (*Engine, *session.Operation, func(), *catalog.Type, *catalog.Relation, *CatalogStage, *PreparedCatalogPublication) {
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
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}}}})
	e.Mu.Unlock()
	e.Visibility.SetCommitSeqLocked(1)
	stage := e.BeginCatalogStage(view)
	next := *d
	next.DomainChecks = slices.Clone(d.DomainChecks)
	next.DomainChecks[0].NotValid = false
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	stage.DomainBases = []catalog.Descriptor{d}
	stage.DomainAlterState().Validation = domainAlterUnitFence(t, e, view, d.Address())
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
				state := catalog.CloneState(e.Catalog.State)
				changed := *d
				changed.ObjectRevision++
				if kind == "owner" {
					aclUnitRoles(e, "domain_alter_owner")
					changed.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("domain_alter_owner")
				}
				state.SetDescriptor(d.Address(), &changed)
				e.Catalog.PublishLocked(state)
			case "root", "length", "generation", "ends":
				store := *e.Storage.Relations[table.Address()]
				switch kind {
				case "length":
					store.Versions = append(slices.Clone(store.Versions), storage.RowVersion{Row: 2, Version: 2})
				case "generation":
					store.Generation++
				case "ends":
					store.Ends = &storage.DeletionRoot{}
				}
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(table.Address(), &store)
				e.Mu.Unlock()
			case "replacement":
				state := catalog.CloneState(e.Catalog.State)
				state.SetEpoch(state.Epoch() + 1)
				e.Catalog.PublishLocked(state)
			case "provider":
				stage.DomainBases = append(stage.DomainBases, e.Catalog.State.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}))
				state := catalog.CloneState(e.Catalog.State)
				provider := *state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
				provider.ObjectRevision++
				state.SetDescriptor(provider.Address(), &provider)
				e.Catalog.PublishLocked(state)
			}
			before := e.Catalog.State
			stores := maps.Clone(e.Storage.Relations)
			roots := maps.Clone(e.Storage.IndexRoots)
			_, err := publishCatalog(e, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if kind == "success" || kind == "unrelated" {
				if err != nil || domainUnitType(t, e.CatalogRead(), "d").DomainChecks[0].NotValid {
					t.Fatal("validity not atomically installed", err)
				}
			} else {
				if err == nil || e.Catalog.State != before || !maps.Equal(e.Storage.Relations, stores) || !maps.Equal(e.Storage.IndexRoots, roots) || !d.DomainChecks[0].NotValid {
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
			SetPreparedCatalogPublicationCtx(prepared, barrier)
			before, store := e.Catalog.State, e.Storage.Relations[table.Address()]
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
				_, err := publishCatalog(e, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
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
				if !errors.Is(err, context.Canceled) || e.Catalog.State != before || e.Storage.Relations[table.Address()] != store || !d.DomainChecks[0].NotValid {
					t.Fatal("cancellation partially installed", err)
				}
			} else if err != nil || e.Catalog.State == before || domainUnitType(t, e.CatalogRead(), "d").DomainChecks[0].NotValid || !d.DomainChecks[0].NotValid {
				t.Fatal("successful install retracted", err)
			}
		})
	}
}

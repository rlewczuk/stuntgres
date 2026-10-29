package core_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqlstate"
)

func TestPGInitPrivsAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	foreign := engine.New()
	view := f.e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3394}
	source := view.MustRelation(address)
	ref := f.e.Catalog.Domain.Ref(view.Epoch(), address)
	roles := f.e.RoleStore.PublishedRead(&f.e.Catalog.Owner, view)
	producer := registeredCatalogProducer(3394)
	capture := func(ctx context.Context, v catalog.Read, r authorization.Read, reference ids.ObjectRef, d *catalog.Relation, reject bool) {
		t.Helper()
		f.e.Mu.Lock()
		rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: v, Roles: r, Ref: reference, Source: d, Owner: &f.e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: f.e})}, producer)
		f.e.Mu.Unlock()
		if !reject {
			if err != nil || rows == nil || rows.Count() != view.State().InitialPrivilegeCount() {
				t.Fatal("typed historical capture", rows, err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if err == nil || rows != nil || errors.As(err, &diagnostic) {
			t.Fatal("invalid source accepted or fabricated SQLSTATE", rows, err)
		}
	}
	background := context.Background()
	capture(background, view, roles, ref, source, false)
	capture(background, foreign.CatalogRead(), roles, ref, source, true)
	capture(background, view, foreign.Roles(), ref, source, true)
	capture(background, view, roles, ref, nil, true)
	forged := ref
	forged.Epoch++
	capture(background, view, roles, forged, source, true)
	forged = foreign.Catalog.Domain.Ref(ref.Epoch, ref.Address)
	capture(background, view, roles, forged, source, true)
	detached := *source
	capture(background, view, roles, ref, &detached, true)
	for i := range source.Columns {
		next := catalog.CloneState(view.State())
		invalid := *source
		invalid.Columns = slices.Clone(source.Columns)
		invalid.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(source.Columns[i].Usage.Type.ID())
		next.SetDescriptor(address, &invalid)
		capture(background, f.e.Catalog.Read(next), roles, ref, &invalid, true)
	}
	forged = ref
	forged.Address.SubID = 1
	capture(background, view, roles, forged, source, true)
	for _, width := range []int{0, 4, 6} {
		next := catalog.CloneState(view.State())
		invalid := *source
		invalid.Columns = make([]catalog.Column, width)
		next.SetDescriptor(address, &invalid)
		capture(background, f.e.Catalog.Read(next), roles, ref, &invalid, true)
	}
	original := view.State().InitialPrivilege(address)
	if original == nil || original.PrivType != 'i' || len(original.ACL.Entries) != 2 {
		t.Fatal("missing independently seeded self snapshot")
	}
	for _, corruption := range []string{"nil", "kind", "implicit", "empty", "missing", "column", "grantor", "grantee", "mask", "option"} {
		next := catalog.CloneState(view.State())
		records := maps.Collect(view.State().InitialPrivileges())
		broken := *original
		broken.ACL.Entries = slices.Clone(original.ACL.Entries)
		key := address
		switch corruption {
		case "nil":
			records[key] = nil
		case "kind":
			broken.PrivType = 'x'
		case "implicit":
			broken.ACL.Explicit = false
		case "empty":
			broken.ACL.Entries = nil
		case "missing":
			key.ObjectID = 4294967295
		case "column":
			key.SubID = 6
		case "grantor":
			broken.ACL.Entries[0].Grantor = foreign.RoleStore.Domain.Principal(original.ACL.Entries[0].Grantor.ID())
		case "grantee":
			broken.ACL.Entries[0].Grantee.Role = foreign.RoleStore.Domain.Principal(original.ACL.Entries[0].Grantee.Role.ID())
		case "mask":
			broken.ACL.Entries[0].Privileges |= authorization.ExecutePrivilege
		case "option":
			broken.ACL.Entries[0].GrantOptions = authorization.ExecutePrivilege
		}
		if corruption != "nil" {
			delete(records, address)
			records[key] = &broken
		}
		next.SetInitialPrivileges(records)
		capture(background, f.e.Catalog.Read(next), roles, ref, source, true)
		if view.State().InitialPrivilege(address) != original || original.PrivType != 'i' {
			t.Fatal("corruption fixture changed original backing")
		}
	}
	state, roleRoot, frontier := f.e.Catalog.State, f.e.RoleStore.Published, f.e.Catalog.NextOID()
	for _, at := range []int{1, 2, 3} {
		ctx, cancel := context.WithCancel(background)
		controlled := &namespaceCancelContext{Context: ctx, cancel: cancel, at: at}
		f.e.Mu.Lock()
		rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: controlled, Catalog: view, Roles: roles, Ref: ref, Source: source, Owner: &f.e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: f.e})}, producer)
		f.e.Mu.Unlock()
		cancel()
		if rows != nil || !errors.Is(err, context.Canceled) || f.e.Catalog.State != state || f.e.RoleStore.Published != roleRoot || f.e.Catalog.NextOID() != frontier {
			t.Fatal("canceled capture exported partial rows or changed state", at, rows, err)
		}
	}
	capture(background, view, roles, ref, source, false)
}

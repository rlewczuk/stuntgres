package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoApplicableRolesCaptureOwnership(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := applicableRolesUnitInput(t, f, view, f.reader)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoApplicableRolesOID)
	assertRejected := func(bad pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid producer capture accepted", rows, err)
		}
		if source, err := catalogrt.CaptureRows(bad, produce); source != nil || err == nil {
			t.Fatal("invalid wrapped capture accepted", source, err)
		}
	}
	foreign := engine.New()
	for _, tc := range []struct {
		name   string
		mutate func(*pgcatalog.Input)
	}{
		{"owner", func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner }},
		{"roles", func(in *pgcatalog.Input) { in.Roles = foreign.Roles() }},
		{"catalog", func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() }},
		{"mixed_same_owner_roots", func(in *pgcatalog.Input) {
			in.Catalog = f.e.Catalog.Read(catalog.CloneState(view.State()))
		}},
		{"epoch", func(in *pgcatalog.Input) { in.Ref.Epoch++ }},
		{"foreign_ref", func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) }},
		{"subid", func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 }},
		{"source_copy", func(in *pgcatalog.Input) { copied := *in.Source; in.Source = &copied }},
		{"missing_role_record", func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(runtime)
		}},
		{"foreign_actor", func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = core.NewCatalogRuntime(runtime)
		}},
		{"deleted_actor", func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.State.CurrentUser = f.e.RoleStore.Domain.Principal(999999)
			in.Runtime = core.NewCatalogRuntime(runtime)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := in
			tc.mutate(&bad)
			assertRejected(bad)
		})
	}
	for _, malformed := range []string{"width", "name", "base", "wrong_domain", "foreign_domain", "base_typmod"} {
		for i := range 3 {
			if malformed == "base_typmod" && i < 2 {
				continue // Only the VARCHAR-backed domain has a sized base.
			}
			t.Run(malformed+string(rune('0'+i)), func(t *testing.T) {
				state := catalog.CloneState(view.State())
				source := *in.Source
				source.Columns = append([]catalog.Column(nil), source.Columns...)
				domainID, baseID := sqltype.ID(13248), sqltype.Name
				if i == 2 {
					domainID, baseID = 13256, sqltype.VarChar
				}
				switch malformed {
				case "width":
					source.Columns = source.Columns[:i]
				case "name":
					source.Columns[i].Name = "not_projected"
				case "base":
					source.Columns[i].Usage.Type, _ = view.LookupType(baseID)
				case "wrong_domain":
					source.Columns[i].Usage.Type, _ = view.LookupType(13243)
				case "foreign_domain":
					source.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(domainID)
				case "base_typmod":
					domain, _ := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domainID)})
					copied := *domain
					base := copied.SQLType.DomainBase()
					base.Typmod = 11
					var err error
					copied.SQLType, err = copied.SQLType.RebindDomain(base, copied.SQLType.Name(), copied.SQLType.Name())
					if err != nil {
						t.Fatal(err)
					}
					state.SetDescriptor(copied.Address(), &copied)
					// Both identifier columns resolve to this same captured domain.
					for j := range source.Columns {
						if source.Columns[j].Usage.Type.ID() == domainID {
							source.Columns[j].Usage.Type = copied.SQLType
						}
					}
				}
				state.SetDescriptor(source.Address(), &source)
				assertRejected(applicableRolesUnitInput(t, f, f.e.Catalog.Read(state), f.reader))
			})
		}
	}
	t.Run("missing_database_owner", func(t *testing.T) {
		state := catalog.CloneState(view.State())
		database, _ := view.Database(catalog.CurrentDatabaseAddress)
		copied := *database
		copied.Owner = f.e.RoleStore.Domain.Principal(999999)
		state.SetDescriptor(copied.Address(), &copied)
		assertRejected(applicableRolesUnitInput(t, f, f.e.Catalog.Read(state), f.reader))
	})
	t.Run("missing_owner_role", func(t *testing.T) {
		roles := cloneRoleFixture(f.e.RoleStore.Published)
		retireFixtureRole(roles, f.e.Roles().LookupRef("pg_database_owner"))
		f.e.RoleStore.PublishLocked(roles)
		assertRejected(applicableRolesUnitInput(t, f, view, f.reader))
	})
	// Restore the predefined role, then exercise every deterministic Err boundary
	// for an ordinary closure and the superuser scan, including duplicate/hidden
	// edges. Each fresh budget cancels at the next boundary, never on a timer.
	seed := authorization.Seed(&f.e.RoleStore.Domain)
	live := cloneRoleFixture(f.e.RoleStore.Published)
	ownerRole, _ := seed.Read(authorization.PrincipalRef{}).Lookup("pg_database_owner")
	putFixtureRole(live, ownerRole)
	bootstrap := f.e.Roles().LookupRef("postgres")
	setFixtureEdges(live, []authorization.Membership{
		{Role: f.owner, Member: f.reader, Grantor: bootstrap, Inherit: true},
		{Role: f.owner, Member: f.reader, Grantor: f.member, Inherit: true},
		{Role: f.member, Member: f.owner, Grantor: bootstrap},
		{Role: bootstrap, Member: f.member, Grantor: bootstrap},
	})
	f.e.RoleStore.PublishLocked(live)
	for _, actor := range []authorization.PrincipalRef{f.reader, bootstrap} {
		completed := false
		for remaining := 1; remaining <= 128; remaining++ {
			canceled := applicableRolesUnitInput(t, f, view, actor)
			canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
			rows, err := produce(canceled)
			if err == nil {
				if rows == nil || remaining <= 5 {
					t.Fatal("incomplete cancellation traversal", remaining, rows)
				}
				completed = true
				break
			}
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation exposed prefix or wrong error", remaining, rows, err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("context error fabricated SQLSTATE", state.SQLState())
			}
			canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
			if source, err := catalogrt.CaptureRows(canceled, produce); source != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("wrapped cancellation exposed source", remaining, source, err)
			}
		}
		if !completed {
			t.Fatal("deterministic cancellation did not reach successful capture")
		}
	}
}

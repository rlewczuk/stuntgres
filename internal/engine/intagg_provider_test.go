package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"testing"
)

func intaggUnitInstalled(t *testing.T) (*Engine, *catalog.Extension) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "intagg", ""))
	return e, e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intagg"}))
}

func TestIntaggProviderManifest(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	registration := e.Catalog.Extensions["intagg"].Control()
	if registration.Version != "1.1" || registration.Trusted || !registration.Superuser || !registration.Relocatable || registration.Comment == nil || *registration.Comment != "integer aggregator and enumerator (obsolete)" {
		t.Fatal("released security metadata", registration)
	}
	if len(ext.Members) != 2 {
		t.Fatal("documented manifest", ext.Members)
	}
	if err := extension.StockIntagg.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	addresses := map[ids.ObjectAddress]bool{}
	for i, address := range ext.Members {
		proc := view.MustProcedure(address)
		member := extension.IntaggMembers[i]
		if addresses[address] || proc.Name() != member.Name || proc.Signature != member.Signature || proc.Result != member.Result || proc.Strict != member.Strict || proc.Owner != ext.Owner {
			t.Fatal("member identity", proc)
		}
		addresses[address] = true
		if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(view, proc); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.ObjectName = "int_agg_state" }, func(p *catalog.Procedure) { p.Signature = "25" },
			func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Strict = !p.Strict },
			func(p *catalog.Procedure) { p.Volatility = expr.Stable }, func(p *catalog.Procedure) { p.ObjectRevision = 0 },
			func(p *catalog.Procedure) { p.RoutineKind = catalog.ProcedureRoutine },
			func(p *catalog.Procedure) { p.Owner = authorization.PrincipalRef{} },
			func(p *catalog.Procedure) { p.ObjectNamespace = ids.ObjectAddress{} }, func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} },
			func(p *catalog.Procedure) { p.Implementation = extension.StockFuzzystrmatch }, func(p *catalog.Procedure) { p.Implementation = extension.StockPgcrypto },
			func(p *catalog.Procedure) { p.Implementation = extension.StockUnaccent }, func(p *catalog.Procedure) { p.Implementation = extension.StockIntarray },
			func(p *catalog.Procedure) {
				p.Implementation = new(*extension.StockIntagg)
			},
		} {
			state := catalog.CloneState(e.Catalog.State)
			bad := *proc
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := extension.StockIntagg.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("corrupt member accepted", bad)
			}
		}
	}
	for _, members := range [][]ids.ObjectAddress{nil, {ext.Members[0]}, {ext.Members[0], ext.Members[0]}, {ext.Members[1], ext.Members[1]}} {
		bad := *ext
		bad.Members = members
		if err := extension.StockIntagg.ValidateExtension(view, &bad); err == nil {
			t.Fatal("corrupt member inventory", members)
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, edge := range edges {
		if edge.Referenced == ext.Address() && edge.DepType == 'e' {
			if !addresses[edge.Dependent] {
				t.Fatal("foreign member dependency")
			}
			count++
		}
	}
	if count != 2 {
		t.Fatal("membership edges", count)
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil || metadata == "" {
		t.Fatal("owner manifest", metadata, err)
	}
	for _, name := range []string{"unaccent", "intarray", "fuzzystrmatch", "pgcrypto"} {
		t.Run("foreign-"+name, func(t *testing.T) {
			other := New()
			op, done := roleOperation(t, other, "postgres")
			defer done()
			extensionUnitPublish(t, other, op, intarrayUnitStage(t, other, op, name, ""))
			installed := other.Catalog.State.MustExtension(other.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
			var member *catalog.Procedure
			for _, address := range installed.Members {
				if proc, ok := other.Catalog.State.Procedure(address); ok {
					member = proc
					break
				}
			}
			if member == nil {
				t.Fatal("missing procedure fixture")
			}
			state := catalog.CloneState(other.Catalog.State)
			bad := *member
			bad.Implementation = extension.StockIntagg
			state.SetDescriptor(member.Address(), &bad)
			if err := other.Catalog.Extensions[name].ValidateExtension(other.Catalog.Read(state), installed); err == nil {
				t.Fatal("foreign intagg provenance admitted by", name)
			}
		})
	}
}

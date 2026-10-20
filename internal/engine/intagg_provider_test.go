package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"testing"
)

func intaggUnitInstalled(t *testing.T) (*Engine, *catalog.Extension) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "intagg", ""))
	return e, e.catalog.state.MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intagg"}))
}

func TestIntaggProviderManifest(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	registration := e.catalog.Extensions["intagg"].Control()
	if registration.Version != "1.1" || registration.Trusted || !registration.Superuser || !registration.Relocatable || registration.Comment == nil || *registration.Comment != "integer aggregator and enumerator (obsolete)" {
		t.Fatal("released security metadata", registration)
	}
	if len(ext.Members) != 2 {
		t.Fatal("documented manifest", ext.Members)
	}
	if err := stockIntagg.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	addresses := map[ids.ObjectAddress]bool{}
	for i, address := range ext.Members {
		proc := view.MustProcedure(address)
		member := intaggMembers[i]
		if addresses[address] || proc.Name() != member.name || proc.Signature != member.signature || proc.Result != member.result || proc.Strict != member.strict || proc.Owner != ext.Owner {
			t.Fatal("member identity", proc)
		}
		addresses[address] = true
		if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, proc); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.ObjectName = "int_agg_state" }, func(p *catalog.Procedure) { p.Signature = "25" },
			func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Strict = !p.Strict },
			func(p *catalog.Procedure) { p.Volatility = expr.Stable }, func(p *catalog.Procedure) { p.ObjectRevision = 0 },
			func(p *catalog.Procedure) { p.RoutineKind = catalog.ProcedureRoutine },
			func(p *catalog.Procedure) { p.Owner = authorization.PrincipalRef{} },
			func(p *catalog.Procedure) { p.ObjectNamespace = ids.ObjectAddress{} }, func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} },
			func(p *catalog.Procedure) { p.Implementation = stockFuzzystrmatch }, func(p *catalog.Procedure) { p.Implementation = stockPgcrypto },
			func(p *catalog.Procedure) { p.Implementation = stockUnaccent }, func(p *catalog.Procedure) { p.Implementation = stockIntarray },
			func(p *catalog.Procedure) {
				p.Implementation = &intaggProvider{control: catalog.ExtensionControl{Version: "1.1"}}
			},
		} {
			state := catalog.CloneState(e.catalog.state)
			bad := *proc
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := stockIntagg.ValidateExtension(e.catalog.read(state), ext); err == nil {
				t.Fatal("corrupt member accepted", bad)
			}
		}
	}
	for _, members := range [][]ids.ObjectAddress{nil, {ext.Members[0]}, {ext.Members[0], ext.Members[0]}, {ext.Members[1], ext.Members[1]}} {
		bad := *ext
		bad.Members = members
		if err := stockIntagg.ValidateExtension(view, &bad); err == nil {
			t.Fatal("corrupt member inventory", members)
		}
	}
	edges, err := collectPGDepend(context.Background(), view, engineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, edge := range edges {
		if edge.referenced == ext.Address() && edge.deptype == 'e' {
			if !addresses[edge.dependent] {
				t.Fatal("foreign member dependency")
			}
			count++
		}
	}
	if count != 2 {
		t.Fatal("membership edges", count)
	}
	metadata, err := extensionOwnerMetadata(view, e.roles.read(e.catalog.owner(), view, e.roles.published), ext)
	if err != nil || metadata == "" {
		t.Fatal("owner manifest", metadata, err)
	}
	for _, name := range []string{"unaccent", "intarray", "fuzzystrmatch", "pgcrypto"} {
		t.Run("foreign-"+name, func(t *testing.T) {
			other := New()
			op, done := roleOperation(t, other, "postgres")
			defer done()
			extensionUnitPublish(t, other, op, intarrayUnitStage(t, other, op, name, ""))
			installed := other.catalog.state.MustExtension(other.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
			var member *catalog.Procedure
			for _, address := range installed.Members {
				if proc, ok := other.catalog.state.Procedure(address); ok {
					member = proc
					break
				}
			}
			if member == nil {
				t.Fatal("missing procedure fixture")
			}
			state := catalog.CloneState(other.catalog.state)
			bad := *member
			bad.Implementation = stockIntagg
			state.SetDescriptor(member.Address(), &bad)
			if err := other.catalog.Extensions[name].ValidateExtension(other.catalog.read(state), installed); err == nil {
				t.Fatal("foreign intagg provenance admitted by", name)
			}
		})
	}
}

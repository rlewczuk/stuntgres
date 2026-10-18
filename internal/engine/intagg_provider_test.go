package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func intaggUnitInstalled(t *testing.T) (*Engine, *extensionDescriptor) {
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
	registration := e.catalog.extensions["intagg"]
	if registration.version != "1.1" || registration.trusted || !registration.superuser || !registration.relocatable || registration.comment == nil || *registration.comment != "integer aggregator and enumerator (obsolete)" {
		t.Fatal("released security metadata", registration)
	}
	if len(ext.members) != 2 {
		t.Fatal("documented manifest", ext.members)
	}
	if err := stockIntagg.validate(view, ext); err != nil {
		t.Fatal(err)
	}
	addresses := map[ids.ObjectAddress]bool{}
	for i, address := range ext.members {
		proc := view.MustProcedure(address)
		member := intaggMembers[i]
		if addresses[address] || proc.name != member.name || proc.signature != member.signature || proc.result != member.result || proc.strict != member.strict || proc.owner != ext.owner {
			t.Fatal("member identity", proc)
		}
		addresses[address] = true
		if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, proc); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*procedureDescriptor){
			func(p *procedureDescriptor) { p.name = "int_agg_state" }, func(p *procedureDescriptor) { p.signature = "25" },
			func(p *procedureDescriptor) { p.result = 25 }, func(p *procedureDescriptor) { p.strict = !p.strict },
			func(p *procedureDescriptor) { p.volatility = Stable }, func(p *procedureDescriptor) { p.revision = 0 },
			func(p *procedureDescriptor) { p.routineKind = procedureRoutine },
			func(p *procedureDescriptor) { p.owner = authorization.PrincipalRef{} },
			func(p *procedureDescriptor) { p.namespace = ids.ObjectAddress{} }, func(p *procedureDescriptor) { p.extension = ids.ObjectAddress{} },
			func(p *procedureDescriptor) { p.fuzzy = stockFuzzystrmatch }, func(p *procedureDescriptor) { p.crypto = stockPgcrypto },
			func(p *procedureDescriptor) { p.provider = stockUnaccent }, func(p *procedureDescriptor) { p.queryCodec = stockIntarray },
			func(p *procedureDescriptor) { p.intagg = &intaggProvider{version: "1.1"} },
		} {
			state := cloneCatalog(e.catalog.state)
			bad := *proc
			mutate(&bad)
			state.descriptors[address] = &bad
			if err := stockIntagg.validate(e.catalog.read(state), ext); err == nil {
				t.Fatal("corrupt member accepted", bad)
			}
		}
	}
	for _, members := range [][]ids.ObjectAddress{nil, {ext.members[0]}, {ext.members[0], ext.members[0]}, {ext.members[1], ext.members[1]}} {
		bad := *ext
		bad.members = members
		if err := stockIntagg.validate(view, &bad); err == nil {
			t.Fatal("corrupt member inventory", members)
		}
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, edge := range edges {
		if edge.referenced == ext.address && edge.deptype == 'e' {
			if !addresses[edge.dependent] {
				t.Fatal("foreign member dependency")
			}
			count++
		}
	}
	if count != 2 {
		t.Fatal("membership edges", count)
	}
	metadata, err := extensionOwnerMetadata(view, e.roleRead(view, e.roles), ext)
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
			var member *procedureDescriptor
			for _, address := range installed.members {
				if proc, ok := other.catalog.state.Procedure(address); ok {
					member = proc
					break
				}
			}
			if member == nil {
				t.Fatal("missing procedure fixture")
			}
			state := cloneCatalog(other.catalog.state)
			bad := *member
			bad.intagg = stockIntagg
			state.descriptors[member.address] = &bad
			if err := other.catalog.extensions[name].provider.validate(other.catalog.read(state), installed); err == nil {
				t.Fatal("foreign intagg provenance admitted by", name)
			}
		})
	}
}

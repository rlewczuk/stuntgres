package schemacmds_test

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"testing"
)

func TestLtreeLqueryProvider(t *testing.T) {
	e, ext, _ := ltreeUnitInstalled(t)
	view := e.CatalogRead()
	if len(ext.Installation.Members) != 94 || len(ext.Installation.Declarations) != 94 || len(ext.Members) != 97 || extension.LtreeLqueryProcedureStart != 86 || extension.LtreeLqueryOperatorStart != 88 || extension.LtreeLqueryMemberEnd != 90 || extension.LtreePathMemberEnd != 86 || extension.LtreeHierarchyMemberEnd != 64 {
		t.Fatal("appended roles moved earlier members")
	}
	if err := extension.StockLtree.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"ltq_regex", "ltq_rregex"} {
		proc := view.MustProcedure(ext.Installation.Members[extension.LtreeLqueryProcedureStart+i])
		if symbol, err := extension.ProcedureSymbol(view, proc); err != nil || symbol != name {
			t.Fatal("genuine lquery declaration", symbol, err)
		}
		operation, ok := extension.LtreeLqueryProcedure(view, proc)
		if !ok || int(operation) != i {
			t.Fatal("selected scalar role lost")
		}
		path, query := view.MustType(ext.Installation.Members[3]).SQLType, view.MustType(ext.Installation.Members[0]).SQLType
		left, right := path, query
		if i == 1 {
			left, right = right, left
		}
		if operation.Validate(left, right, view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType) != nil {
			t.Fatal("wrong genuine formals")
		}
		for _, change := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Signature = "25,25" }, func(p *catalog.Procedure) { p.Result = 25 },
			func(p *catalog.Procedure) { p.Display.Source = "ltree_out" }, func(p *catalog.Procedure) { p.ObjectNamespace = ids.ObjectAddress{} },
			func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Implementation = extension.StockCitext },
		} {
			state := catalog.CloneState(view.State())
			bad := *proc
			change(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("corrupt lquery procedure admitted")
			}
			if err := extension.StockLtree.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("corrupt lquery procedure authority admitted")
			}
		}
		op, _ := view.Operator(ext.Installation.Members[extension.LtreeLqueryOperatorStart+i])
		for _, change := range []func(*catalog.Operator){
			func(o *catalog.Operator) {
				o.Function = ext.Installation.Members[extension.LtreeComparisonProcedureStart].ObjectID
			},
			func(o *catalog.Operator) { o.Commutator = o.Address().ObjectID }, func(o *catalog.Operator) { o.Restriction = 0 },
			func(o *catalog.Operator) { o.Join = 0 }, func(o *catalog.Operator) { o.Left = 25 }, func(o *catalog.Operator) { o.Right = 25 },
			func(o *catalog.Operator) { o.Result = 25 }, func(o *catalog.Operator) { o.Hash = true }, func(o *catalog.Operator) { o.Merge = true },
			func(o *catalog.Operator) { o.Negator = o.Address().ObjectID }, func(o *catalog.Operator) { o.Extension = ids.ObjectAddress{} },
		} {
			state := catalog.CloneState(view.State())
			bad := *op
			change(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("corrupt lquery operator admitted")
			}
			if bad.Extension == (ids.ObjectAddress{}) {
				ltreeUnitDetachedOperator(t, e.Catalog.Read(state), ext, &bad)
			} else if err := (routine.OperatorProvider{}).Validate(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("corrupt lquery operator authority admitted")
			}
		}
		state := catalog.CloneState(view.State())
		state.RemoveDescriptor(proc.Address())
		if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
			t.Fatal("missing member admitted")
		}
	}
	for _, slots := range [][2]int{{86, 87}, {86, 60}, {88, 89}, {88, 62}} {
		state := catalog.CloneState(view.State())
		bad := *ext
		forged := *ext.Installation
		forged.Members = append([]ids.ObjectAddress(nil), forged.Members...)
		forged.Members[slots[0]], forged.Members[slots[1]] = forged.Members[slots[1]], forged.Members[slots[0]]
		bad.Installation = &forged
		state.SetDescriptor(bad.Address(), &bad)
		if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state), &bad); err == nil {
			t.Fatal("forged immutable appended role admitted", slots)
		}
	}
	if e.CatalogRead().MustExtension(ext.Address()) != ext {
		t.Fatal("private rejection mutated publication")
	}
}

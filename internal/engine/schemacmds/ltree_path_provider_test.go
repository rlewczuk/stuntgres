package schemacmds_test

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func TestLtreePathProviderIdentity(t *testing.T) {
	e, ext, _ := ltreeUnitInstalled(t)
	view := e.CatalogRead()
	recipe := ext.Installation
	if extension.LtreePathProcedureStart != 64 || extension.LtreePathOperatorStart != 83 || extension.LtreePathMemberEnd != 86 || len(recipe.Members) != 94 || len(recipe.Declarations) != 94 || len(ext.Members) != 97 {
		t.Fatal("immutable recipe/current companion inventory moved incumbent roles")
	}
	if err := extension.StockLtree.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	names := []string{"subltree", "subpath", "subpath", "index", "index", "nlevel", "ltree2text", "text2ltree", "lca", "lca", "lca", "lca", "lca", "lca", "lca", "lca", "ltree_addltree", "ltree_addtext", "ltree_textadd"}
	for i, name := range names {
		proc := view.MustProcedure(recipe.Members[extension.LtreePathProcedureStart+i])
		if symbol, err := extension.ProcedureSymbol(view, proc); err != nil || symbol != extension.LtreePathProcedureSymbol(sqltype.LtreePathOperation(i)) {
			t.Fatal("genuine symbol", symbol, err)
		}
		if installed, err := extension.ProcedureInstallationName(view, proc); err != nil || installed != name {
			t.Fatal("SQL name differs from C symbol", installed, name, err)
		}
		if operation, ok := extension.LtreePathProcedure(view, proc); !ok || operation != sqltype.LtreePathOperation(i) {
			t.Fatal("genuine path role lost", i)
		}
		for _, change := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Signature = "25,25,25" }, func(p *catalog.Procedure) { p.Result = 16 },
			func(p *catalog.Procedure) { p.Display.Source = "ltree_out" }, func(p *catalog.Procedure) { p.ObjectNamespace = ids.ObjectAddress{} },
			func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Implementation = extension.StockCitext },
		} {
			state := catalog.CloneState(view.State())
			bad := *proc
			change(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			current := view.WithState(state)
			if err := extension.StockLtree.ValidateExtension(current, ext); err == nil {
				t.Fatal("corrupt path procedure admitted", i)
			}
			if err := extension.StockLtree.ValidateProcedure(current, &bad); err == nil {
				t.Fatal("corrupt path procedure authority admitted", i)
			}
			if i >= int(sqltype.LtreeConcat) {
				op, _ := current.Operator(recipe.Members[extension.LtreePathOperatorStart+i-int(sqltype.LtreeConcat)])
				if err := (routine.OperatorProvider{}).Validate(current, op); err == nil {
					t.Fatal("forged backer granted operator authority", i)
				}
			}
		}
		state := catalog.CloneState(view.State())
		state.RemoveDescriptor(proc.Address())
		if err := extension.StockLtree.ValidateExtension(view.WithState(state), ext); err == nil {
			t.Fatal("missing current member admitted")
		}
	}
	for i := range 3 {
		op, ok := view.Operator(recipe.Members[extension.LtreePathOperatorStart+i])
		if !ok {
			t.Fatal("missing concat operator")
		}
		if err := (routine.OperatorProvider{}).Validate(view, op); err != nil {
			t.Fatal("genuine concat operator", i, err)
		}
		movedState := catalog.CloneState(view.State())
		moved := *op
		moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		movedState.UnbindName(catalog.DescriptorNameKey(op))
		movedState.SetDescriptor(moved.Address(), &moved)
		movedState.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
		movedView := view.WithState(movedState)
		if err := (routine.OperatorProvider{}).Validate(movedView, &moved); err != nil {
			t.Fatal("moved operator provider", i, err)
		}
		if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(movedView, ext); err != nil {
			t.Fatal("moved installation provider", i, err)
		}
		copied := *ext
		copied.ObjectRevision = ext.Revision() + 1
		if err := extension.StockLtree.ValidateExtension(movedView, &copied); err != nil {
			t.Fatal("unpublished candidate recipe rejected", err)
		}
		provider := extension.CatalogProvider{Class: ids.ExtensionClassID}
		if err := provider.Validate(movedView, &copied); err == nil {
			t.Fatal("copied live header granted authority")
		}
		candidateState := catalog.CloneState(movedView.State())
		candidateState.SetDescriptor(copied.Address(), &copied)
		if err := provider.Validate(movedView.WithState(candidateState), &copied); err != nil {
			t.Fatal("resident unpublished header rejected", err)
		}
		foreign := copied
		foreign.Owner = (&authorization.Domain{}).Principal(copied.Owner.ID())
		candidateState.SetDescriptor(foreign.Address(), &foreign)
		if err := provider.Validate(movedView.WithState(candidateState), &foreign); err == nil {
			t.Fatal("foreign current header principal granted authority")
		}
		for _, change := range []func(*catalog.Operator){
			func(o *catalog.Operator) { o.Function = recipe.Members[extension.LtreePathProcedureStart].ObjectID },
			func(o *catalog.Operator) { o.Commutator = o.Address().ObjectID }, func(o *catalog.Operator) { o.Restriction = 5040 }, func(o *catalog.Operator) { o.Join = 5041 },
			func(o *catalog.Operator) { o.Left = 16 }, func(o *catalog.Operator) { o.Right = 16 }, func(o *catalog.Operator) { o.Result = 25 },
			func(o *catalog.Operator) { o.Hash = true }, func(o *catalog.Operator) { o.Merge = true }, func(o *catalog.Operator) { o.Negator = o.Address().ObjectID },
			func(o *catalog.Operator) { o.Extension = ids.ObjectAddress{} },
		} {
			state := catalog.CloneState(view.State())
			bad := *op
			change(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if err := extension.StockLtree.ValidateExtension(view.WithState(state), ext); err == nil {
				t.Fatal("corrupt concat operator admitted", i)
			}
			if bad.Extension == (ids.ObjectAddress{}) {
				ltreeUnitDetachedOperator(t, view.WithState(state), ext, &bad)
			} else if err := (routine.OperatorProvider{}).Validate(view.WithState(state), &bad); err == nil {
				t.Fatal("forged operator authority admitted", i)
			}
		}
	}
	for _, change := range []func(*catalog.Extension){
		func(x *catalog.Extension) {
			x.Members = append([]ids.ObjectAddress(nil), x.Members[:len(x.Members)-1]...)
		},
		func(x *catalog.Extension) {
			x.Members = append(append([]ids.ObjectAddress(nil), x.Members...), x.Members[0])
		},
		func(x *catalog.Extension) {
			x.Members = append([]ids.ObjectAddress(nil), x.Members...)
			x.Members[64] = x.Members[65]
		},
	} {
		state := catalog.CloneState(view.State())
		candidate := *ext
		change(&candidate)
		state.SetDescriptor(candidate.Address(), &candidate)
		if err := extension.StockLtree.ValidateExtension(view.WithState(state), &candidate); err == nil {
			t.Fatal("nonreciprocal current membership admitted")
		}
	}
	if e.CatalogRead().MustExtension(ext.Address()) != ext {
		t.Fatal("rejection changed incumbent")
	}
}

func TestLtreePathQuerySurvivorAuthority(t *testing.T) {
	e, op, origin, _ := lqueryProviderInstalled(t, false)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "uuid-ossp", "public"))
	view := e.CatalogRead()
	recipe := origin.Installation
	enginetest.RoleExecute(t, e, op, "DROP EXTENSION ltree")
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "ltree", "public"))
	other := e.CatalogRead().MustExtension(e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"})).Installation
	for _, absent := range []bool{false, true} {
		for _, foreign := range []bool{false, true} {
			state := catalog.CloneState(view.State())
			receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
			received, detached := *receiver, *origin
			received.Members = slices.Clone(receiver.Members)
			detached.Members = nil
			for _, address := range origin.Members {
				d := state.Get(address)
				membership := ids.ObjectAddress{}
				if foreign {
					membership = receiver.Address()
					received.Members = append(received.Members, address)
				}
				next, err := catalog.WithExtensionMembership(d, membership, d.Revision())
				if err != nil {
					t.Fatal(err)
				}
				state.SetDescriptor(address, next)
			}
			state.SetDescriptor(received.Address(), &received)
			state.SetDescriptor(detached.Address(), &detached)
			if absent {
				state.UnbindName(catalog.DescriptorNameKey(origin))
				state.RemoveDescriptor(origin.Address())
			}
			current := view.WithState(state)
			if !absent {
				if err := extension.StockLtree.ValidateExtension(current, &detached); err != nil {
					t.Fatal("empty current membership", err)
				}
			}
			for _, family := range []struct{ start, count, operators, firstOperator int }{
				{extension.LtreePathProcedureStart, int(sqltype.LtreePathOperationCount), extension.LtreePathOperatorStart, int(sqltype.LtreeConcat)},
				{extension.LtreeLqueryProcedureStart, int(sqltype.LtreeLqueryOperationCount), extension.LtreeLqueryOperatorStart, 0},
				{extension.LtreeLtxtqueryProcedureStart, int(sqltype.LtreeLtxtqueryOperationCount), extension.LtreeLtxtqueryOperatorStart, 0},
			} {
				for index := range family.count {
					proc := current.MustProcedure(recipe.Members[family.start+index])
					if err := extension.StockLtree.ValidateProcedure(current, proc); err != nil {
						t.Fatal("surviving procedure", absent, foreign, index, err)
					}
					if family.start == extension.LtreePathProcedureStart {
						if operation, ok := extension.LtreePathProcedure(current, proc); !ok || int(operation) != index {
							t.Fatal("path role lost", index)
						}
					} else if family.start == extension.LtreeLqueryProcedureStart {
						if operation, ok := extension.LtreeLqueryProcedure(current, proc); !ok || int(operation) != index {
							t.Fatal("query role lost", index)
						}
					} else if operation, ok := extension.LtreeLtxtqueryProcedure(current, proc); !ok || int(operation) != index {
						t.Fatal("text query role lost", index)
					}
					var operator *catalog.Operator
					if index >= family.firstOperator {
						operator, _ = current.Operator(recipe.Members[family.operators+index-family.firstOperator])
						if err := (routine.OperatorProvider{}).Validate(current, operator); err != nil {
							t.Fatal("surviving operator", err)
						}
					}
					for _, cause := range []string{"copied_origin", "foreign_cohort", "missing_origin", "principal", "source", "signature", "result", "provider", "declaration"} {
						broken := catalog.CloneState(state)
						bad := *proc
						switch cause {
						case "copied_origin":
							copied := *recipe
							bad.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: proc.Origin.Declaration}
						case "foreign_cohort":
							bad.Origin = &catalog.InstalledOrigin{Installation: other, Declaration: proc.Origin.Declaration}
						case "missing_origin":
							bad.Origin = nil
						case "principal":
							bad.Owner = (&authorization.Domain{}).Principal(proc.Owner.ID())
						case "source":
							bad.Display.Source = "ltree_in"
						case "signature":
							bad.Signature = "16"
						case "result":
							bad.Result = 0
						case "provider":
							bad.Implementation = extension.StockCitext
						case "declaration":
							bad.Origin = &catalog.InstalledOrigin{Installation: recipe, Declaration: proc.Origin.Declaration + 1}
						}
						broken.SetDescriptor(bad.Address(), &bad)
						rejected := view.WithState(broken)
						if err := extension.StockLtree.ValidateProcedure(rejected, &bad); err == nil {
							t.Fatal("forged surviving authority", cause, index)
						}
						if operator != nil {
							if err := (routine.OperatorProvider{}).Validate(rejected, operator); err == nil {
								t.Fatal("operator ignored forged backer", cause, index)
							}
						}
					}
				}
			}
		}
	}
}

// Object-local validation accepts a detached provider. The parent extension
// owns the reverse edge and rejects a one-sided detach until its bag is updated.
func ltreeUnitDetachedOperator(t *testing.T, view catalog.Read, ext *catalog.Extension, operator *catalog.Operator) {
	t.Helper()
	provider := extension.CatalogProvider{Class: ids.ExtensionClassID}
	if err := (routine.OperatorProvider{}).Validate(view, operator); err != nil {
		t.Fatal("detached local operator authority rejected", err)
	}
	if err := provider.Validate(view, ext); err == nil {
		t.Fatal("one-sided detach granted parent authority")
	}
	state := catalog.CloneState(view.State())
	detached := *ext
	detached.Members = slices.DeleteFunc(slices.Clone(ext.Members), func(a ids.ObjectAddress) bool { return a == operator.Address() })
	state.SetDescriptor(detached.Address(), &detached)
	current := view.WithState(state)
	if err := provider.Validate(current, &detached); err != nil {
		t.Fatal("coherent detached installation rejected", err)
	}
	if err := (routine.OperatorProvider{}).Validate(current, operator); err != nil {
		t.Fatal("coherent detached operator rejected", err)
	}
	receiver := *current.MustExtension(current.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "plpgsql"}))
	receiver.Members = append(slices.Clone(receiver.Members), operator.Address())
	received := *operator
	received.Extension = receiver.Address()
	state.SetDescriptor(receiver.Address(), &receiver)
	state.SetDescriptor(received.Address(), &received)
	current = view.WithState(state)
	for _, parent := range []*catalog.Extension{&detached, &receiver} {
		if err := provider.Validate(current, parent); err != nil {
			t.Fatal("coherent foreign membership parent rejected", err)
		}
	}
	if err := (routine.OperatorProvider{}).Validate(current, &received); err != nil {
		t.Fatal("foreign membership replaced released operator authority", err)
	}
}

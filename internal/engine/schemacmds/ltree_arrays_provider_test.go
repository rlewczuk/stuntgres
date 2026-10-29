package schemacmds_test

import (
	"context"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestLtreeArrayProvider(t *testing.T) {
	ltreeArrayProviderConflicts(t)
	e, _, ext, _ := lqueryProviderInstalled(t, false)
	view := e.CatalogRead()
	if !ext.Installation.Valid() || len(ext.Installation.Members) != extension.LtreeMemberCount || len(ext.Installation.Declarations) != extension.LtreeMemberCount || len(ext.Members) != len(ext.Installation.Members)+3 || extension.LtreeArrayProcedureStart != 9 || extension.LtreeArrayOperatorStart != 25 {
		t.Fatal("released recipe/current companion inventory")
	}
	for _, slot := range []int{0, 3, 6} {
		base := view.MustType(ext.Installation.Members[slot])
		array, ok := view.LookupArray(base.SQLType)
		if !ok || array.Element() != base.SQLType || array.Concrete() != (slot != 6) {
			t.Fatal("companion activation split")
		}
		if typedef.StoredArrayElement(base.SQLType) != (slot != 6) || typedef.StoredArrayElement(array) != (slot != 6) {
			t.Fatal("published family storage admission differs from concrete companion")
		}
	}
	for index, name := range []string{"_ltree_isparent", "_ltree_r_isparent", "_ltree_risparent", "_ltree_r_risparent", "_ltq_regex", "_ltq_rregex", "lt_q_regex", "lt_q_rregex", "_lt_q_regex", "_lt_q_rregex", "_ltxtq_exec", "_ltxtq_rexec", "_ltree_extract_isparent", "_ltree_extract_risparent", "_ltq_extract_regex", "_ltxtq_extract_exec"} {
		proc := view.MustProcedure(ext.Installation.Members[extension.LtreeArrayProcedureStart+index])
		operator, ok := view.Operator(ext.Installation.Members[extension.LtreeArrayOperatorStart+index])
		if !ok {
			t.Fatal("operator missing")
		}
		symbol, err := extension.ProcedureSymbol(view, proc)
		if err != nil || symbol != name || proc.Display.Source != name || !proc.Strict {
			t.Fatal("selected declaration", symbol, err)
		}
		operation, ok := extension.LtreeArrayProcedure(view, proc)
		if !ok || operation != sqltype.LtreeArrayOperation(index) {
			t.Fatal("selected role")
		}
		if operator.Function != proc.Address().ObjectID || operator.Result != proc.Result || operator.Negator != 0 || operator.Hash || operator.Merge {
			t.Fatal("operator linkage")
		}
		if index < 12 {
			if operator.Commutator != ext.Installation.Members[extension.LtreeArrayOperatorStart+(index^1)].ObjectID {
				t.Fatal("commutator")
			}
			if operator.Restriction != 5040 || operator.Join != 5041 {
				t.Fatal("installed 1.3 matching selectivity")
			}
		} else if operator.Commutator != 0 || operator.Restriction != 0 || operator.Join != 0 {
			t.Fatal("extractor metadata")
		}
	}
	for _, corrupt := range []func(*catalog.State, *catalog.Extension){
		func(_ *catalog.State, d *catalog.Extension) { d.Members = d.Members[:len(d.Members)-1] },
		func(_ *catalog.State, d *catalog.Extension) {
			d.Members = append(d.Members, d.Members[len(d.Members)-1])
		},
		func(_ *catalog.State, d *catalog.Extension) {
			forged := *d.Installation
			forged.Members = slices.Clone(forged.Members)
			forged.Members[9], forged.Members[10] = forged.Members[10], forged.Members[9]
			d.Installation = &forged
		},
		func(s *catalog.State, d *catalog.Extension) { s.RemoveDescriptor(d.Installation.Members[22]) },
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[9])
			p.Signature = "25,25"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[22])
			p.Result = 16
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[9])
			p.Display.Source = "ltree_in"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[9])
			p.Strict = false
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[9])
			p.Implementation = extension.StockHstore
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[9])
			p.Extension = ids.ObjectAddress{}
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Operator(d.Installation.Members[extension.LtreeArrayOperatorStart])
			copy := *o
			copy.Commutator = 0
			s.SetDescriptor(copy.Address(), &copy)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Operator(d.Installation.Members[extension.LtreeArrayOperatorStart])
			copy := *o
			copy.Restriction = 1302
			s.SetDescriptor(copy.Address(), &copy)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Operator(d.Installation.Members[extension.LtreeArrayOperatorStart])
			copy := *o
			copy.Join = 1303
			s.SetDescriptor(copy.Address(), &copy)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Operator(d.Installation.Members[extension.LtreeComparisonProcedureStart-1])
			copy := *o
			copy.Function = d.Installation.Members[9].ObjectID
			s.SetDescriptor(copy.Address(), &copy)
		},
		func(s *catalog.State, d *catalog.Extension) {
			s.RemoveDescriptor(d.Installation.Members[extension.LtreeArrayOperatorStart-1])
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Installation.Members[extension.LtreeArrayProcedureStart+int(sqltype.LtreeArrayFirstQuery)])
			p.Result = ids.OID(view.MustType(d.Installation.Members[0]).SQLType.ID())
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Operator(d.Installation.Members[extension.LtreeComparisonProcedureStart-1])
			copy := *o
			copy.Commutator = d.Installation.Members[extension.LtreeArrayOperatorStart].ObjectID
			s.SetDescriptor(copy.Address(), &copy)
		},
	} {
		candidate := catalog.CloneState(view.State())
		bad := *ext
		bad.Members = slices.Clone(ext.Members)
		candidate.SetDescriptor(bad.Address(), &bad)
		corrupt(candidate, &bad)
		if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(candidate), &bad); err == nil {
			t.Fatal("corrupt selected graph accepted")
		}
		if err := extension.StockLtree.ValidateExtension(view, ext); err != nil {
			t.Fatal("failed validation mutated incumbent", err)
		}
	}
	fresh := engine.New()
	op, done := enginetest.RoleOperation(t, fresh, "postgres")
	defer done()
	stage := lqueryProviderStage(t, fresh, op)
	candidate := fresh.StageRead(stage)
	staged := candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	last, _ := candidate.Operator(staged.Installation.Members[extension.LtreeComparisonProcedureStart-1])
	last.Function = staged.Installation.Members[9].ObjectID
	before := fresh.CatalogRead().State()
	if _, err := fresh.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("late invalid graph admitted", err)
	}
	if fresh.CatalogRead().State() != before {
		t.Fatal("late failure published prefix")
	}
}

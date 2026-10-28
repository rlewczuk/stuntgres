package schemacmds_test

import (
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
)

func TestLtreeHierarchyProvider(t *testing.T){
 e,ext,_:=ltreeUnitInstalled(t)
 view:=e.CatalogRead()
 if len(ext.Members)!=extension.LtreeMemberCount||extension.LtreeHierarchyProcedureStart!=60||extension.LtreeHierarchyOperatorStart!=62||extension.LtreeHierarchyMemberEnd!=64{t.Fatal("appended roles moved earlier members")}
 if err:=extension.StockLtree.ValidateExtension(view,ext);err!=nil{t.Fatal(err)}
 for i,name:=range []string{"ltree_isparent","ltree_risparent"}{
  proc:=view.MustProcedure(ext.Members[extension.LtreeHierarchyProcedureStart+i])
  if symbol,err:=extension.ProcedureSymbol(view,proc);err!=nil||symbol!=name{t.Fatal("genuine hierarchy declaration",symbol,err)}
  for _,change:=range []func(*catalog.Procedure){
   func(p *catalog.Procedure){p.Signature="25,25"},func(p *catalog.Procedure){p.Result=25},
   func(p *catalog.Procedure){p.Display.Source="ltree_out"},func(p *catalog.Procedure){p.ObjectNamespace=ids.ObjectAddress{}},
   func(p *catalog.Procedure){p.Strict=false},func(p *catalog.Procedure){p.Implementation=extension.StockCitext},
  }{
   state:=catalog.CloneState(view.State());bad:=*proc;change(&bad);state.SetDescriptor(bad.Address(),&bad)
   if err:=extension.StockLtree.ValidateExtension(e.Catalog.Read(state),ext);err==nil{t.Fatal("corrupt hierarchy procedure admitted")}
  }
  op,_:=view.Operator(ext.Members[extension.LtreeHierarchyOperatorStart+i])
  for _,change:=range []func(*catalog.Operator){
   func(o *catalog.Operator){o.Function=ext.Members[extension.LtreeComparisonProcedureStart].ObjectID},
   func(o *catalog.Operator){o.Commutator=o.Address().ObjectID},func(o *catalog.Operator){o.Restriction=0},
   func(o *catalog.Operator){o.Join=0},func(o *catalog.Operator){o.Left=25},func(o *catalog.Operator){o.Right=25},
   func(o *catalog.Operator){o.Result=25},func(o *catalog.Operator){o.Hash=true},func(o *catalog.Operator){o.Merge=true},
   func(o *catalog.Operator){o.Negator=o.Address().ObjectID},func(o *catalog.Operator){o.Extension=ids.ObjectAddress{}},
  }{
   state:=catalog.CloneState(view.State());bad:=*op;change(&bad);state.SetDescriptor(bad.Address(),&bad)
   if err:=extension.StockLtree.ValidateExtension(e.Catalog.Read(state),ext);err==nil{t.Fatal("corrupt hierarchy operator admitted")}
  }
  state:=catalog.CloneState(view.State());state.RemoveDescriptor(proc.Address())
  if err:=extension.StockLtree.ValidateExtension(e.Catalog.Read(state),ext);err==nil{t.Fatal("missing member admitted")}
 }
 if e.CatalogRead().MustExtension(ext.Address())!=ext{t.Fatal("private rejection mutated publication")}
}

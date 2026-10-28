package schemacmds_test

import (
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/sqltype"
)

func TestLtreePathProviderIdentity(t *testing.T) {
 e,ext,_ := ltreeUnitInstalled(t); view := e.CatalogRead()
 if extension.LtreePathProcedureStart != 64 || extension.LtreePathOperatorStart != 83 || len(ext.Members) != 86 { t.Fatal("appended inventory moved incumbent roles") }
 if err := extension.StockLtree.ValidateExtension(view,ext); err != nil { t.Fatal(err) }
 names := []string{"subltree","subpath","subpath","index","index","nlevel","ltree2text","text2ltree","lca","lca","lca","lca","lca","lca","lca","lca","ltree_addltree","ltree_addtext","ltree_textadd"}
 for i,name := range names {
  proc := view.MustProcedure(ext.Members[extension.LtreePathProcedureStart+i])
  if symbol,err := extension.ProcedureSymbol(view,proc); err != nil || symbol != extension.LtreePathProcedureSymbol(sqltype.LtreePathOperation(i)) { t.Fatal("genuine symbol",symbol,err) }
  if installed,err := extension.ProcedureInstallationName(view,proc); err != nil || installed != name { t.Fatal("SQL name differs from C symbol",installed,name,err) }
  for _,change := range []func(*catalog.Procedure){
   func(p *catalog.Procedure){p.Signature="25,25,25"},func(p *catalog.Procedure){p.Result=16},
   func(p *catalog.Procedure){p.Display.Source="ltree_out"},func(p *catalog.Procedure){p.ObjectNamespace=ids.ObjectAddress{}},
   func(p *catalog.Procedure){p.Strict=false},func(p *catalog.Procedure){p.Implementation=extension.StockCitext},
  } {
   state := catalog.CloneState(view.State()); bad := *proc; change(&bad); state.SetDescriptor(bad.Address(),&bad)
   if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state),ext); err == nil { t.Fatal("corrupt path procedure admitted",i) }
   if i>=int(sqltype.LtreeConcat) && extension.LtreePathRelocatableOperator(e.Catalog.Read(state),ext,ext.Members[extension.LtreePathOperatorStart+i-int(sqltype.LtreeConcat)]) { t.Fatal("forged backer granted relocation",i) }
  }
  state := catalog.CloneState(view.State()); state.RemoveDescriptor(proc.Address())
  if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state),ext); err == nil { t.Fatal("missing member admitted") }
 }
 for i := range 3 {
  op,ok := view.Operator(ext.Members[extension.LtreePathOperatorStart+i]); if !ok { t.Fatal("missing concat operator") }
  if !extension.LtreePathRelocatableOperator(view,ext,op.Address()) { t.Fatal("genuine path relocation role rejected",i) }
  movedState:=catalog.CloneState(view.State());moved:=*op
  moved.ObjectNamespace=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PGCatalogNamespaceOID}
  movedState.UnbindName(catalog.DescriptorNameKey(op));movedState.SetDescriptor(moved.Address(),&moved);movedState.BindName(catalog.DescriptorNameKey(&moved),moved.Address())
  movedView:=e.Catalog.Read(movedState)
  if !extension.LtreePathRelocatableOperator(movedView,ext,moved.Address()){t.Fatal("genuine moved path relocation rejected",i)}
  if err:=(routine.OperatorProvider{}).Validate(movedView,&moved);err!=nil{t.Fatal("moved operator provider",i,err)}
  if err:=(extension.CatalogProvider{Class:ids.ExtensionClassID}).Validate(movedView,ext);err!=nil{t.Fatal("moved installation provider",i,err)}
  detached:=*ext;detached.ObjectRevision=ext.Revision()+1
  if extension.LtreePathRelocatableOperator(movedView,&detached,moved.Address()){t.Fatal("foreign manifest granted relocation")}
  for _,change := range []func(*catalog.Operator){
   func(o *catalog.Operator){o.Function=ext.Members[extension.LtreePathProcedureStart].ObjectID},
   func(o *catalog.Operator){o.Commutator=o.Address().ObjectID},func(o *catalog.Operator){o.Restriction=5040},func(o *catalog.Operator){o.Join=5041},
   func(o *catalog.Operator){o.Left=16},func(o *catalog.Operator){o.Right=16},func(o *catalog.Operator){o.Result=25},
   func(o *catalog.Operator){o.Hash=true},func(o *catalog.Operator){o.Merge=true},func(o *catalog.Operator){o.Negator=o.Address().ObjectID},
   func(o *catalog.Operator){o.Extension=ids.ObjectAddress{}},
  } {
   state := catalog.CloneState(view.State()); bad := *op; change(&bad); state.SetDescriptor(bad.Address(),&bad)
   if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(state),ext); err == nil { t.Fatal("corrupt concat operator admitted",i) }
   if extension.LtreePathRelocatableOperator(e.Catalog.Read(state),ext,bad.Address()) { t.Fatal("forged operator granted relocation",i) }
  }
 }
 for _,change := range []func(*catalog.Extension){
  func(x *catalog.Extension){x.Members=append([]ids.ObjectAddress(nil),x.Members[:len(x.Members)-1]...)},
  func(x *catalog.Extension){x.Members=append(append([]ids.ObjectAddress(nil),x.Members...),x.Members[0])},
  func(x *catalog.Extension){x.Members=append([]ids.ObjectAddress(nil),x.Members...);x.Members[64]=x.Members[65]},
  func(x *catalog.Extension){x.Members=append([]ids.ObjectAddress(nil),x.Members...);x.Members[64],x.Members[83]=x.Members[83],x.Members[64]},
 } {
  candidate := *ext; change(&candidate)
  if err := extension.StockLtree.ValidateExtension(view,&candidate); err == nil { t.Fatal("malformed manifest admitted") }
  if extension.LtreePathRelocatableOperator(view,&candidate,ext.Members[extension.LtreePathOperatorStart]) { t.Fatal("malformed manifest granted relocation") }
 }
 for _,slot:=range []int{extension.LtreeHierarchyOperatorStart,extension.LtreeComparisonOperatorStart}{if extension.LtreePathRelocatableOperator(view,ext,ext.Members[slot]){t.Fatal("unselected operator granted path relocation",slot)}}
 if e.CatalogRead().MustExtension(ext.Address()) != ext { t.Fatal("rejection changed incumbent") }
}

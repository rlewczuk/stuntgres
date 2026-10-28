package enginetests

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/sqltype"
)

func TestLtreeHierarchyOperation(t *testing.T){
 d:=ltreeArrayUnitTypes(t)
 for _,tc:=range []struct{left,right string;a,b bool}{{"Top","Top.Child",true,false},{"Top.Child","Top",false,true},{"Top","Top",true,true},{"Top","Topical.Child",false,false},{"Top","top.Child",false,false},{"","Top",true,false},{"Top","",false,true},{"","",true,true}}{
  left,right:=ltreeArrayInput(t,d.path,tc.left),ltreeArrayInput(t,d.path,tc.right)
  savedLeft,savedRight:=left,right
  for _,op:=range []sqltype.LtreeHierarchyOperation{sqltype.LtreeAncestor,sqltype.LtreeDescendant}{
   want:=tc.a;if op==sqltype.LtreeDescendant{want=tc.b}
   for range 3{
    value,err:=op.Eval(t.Context(),left,right,d.path,d.path,d.boolean);if err!=nil{t.Fatal(err)}
    got,err:=value.Boolean();if err!=nil||got!=want||value.Type()!=d.boolean{t.Fatal("hierarchy result",got,want,err)}
   }
   for _,args:=range [][2]sqltype.Value{{sqltype.Null(d.path),right},{left,sqltype.Null(d.path)},{sqltype.Null(d.path),sqltype.Null(d.path)}}{
    value,err:=op.Eval(t.Context(),args[0],args[1],d.path,d.path,d.boolean);if err!=nil||!value.IsNull()||value.Type()!=d.boolean{t.Fatal("typed NULL",value,err)}
   }
  }
  if left!=savedLeft||right!=savedRight{t.Fatal("kernel changed immutable backing")}
 }
 foreign:=ltreeArrayUnitTypes(t)
 left:=ltreeArrayInput(t,d.path,"Top")
 for _,op:=range []sqltype.LtreeHierarchyOperation{sqltype.LtreeAncestor,sqltype.LtreeDescendant}{
  for _,value:=range []sqltype.Value{ltreeArrayInput(t,foreign.path,"Top"),sqltype.Null(foreign.path)}{
   for _,args:=range [][2]sqltype.Value{{value,left},{sqltype.Null(d.path),value}}{
    if _,err:=op.Eval(t.Context(),args[0],args[1],d.path,d.path,d.boolean);err==nil{t.Fatal("same-OID foreign operand admitted before NULL")}
   }
  }
  for _,formals:=range [][3]*sqltype.Descriptor{{nil,d.path,d.boolean},{d.path,foreign.path,d.boolean},{d.path,d.path,d.path},{d.query,d.query,d.boolean}}{
   if err:=op.Validate(formals[0],formals[1],formals[2]);err==nil{t.Fatal("invalid formal signature admitted")}
  }
  ctx,cancel:=context.WithCancel(t.Context());cancel()
  if _,err:=op.Eval(ctx,left,left,d.path,d.path,d.boolean);!errors.Is(err,context.Canceled){t.Fatal("ordinary cancellation",err)}
 }
 if err:=sqltype.LtreeHierarchyOperationCount.Validate(d.path,d.path,d.boolean);err==nil{t.Fatal("unknown role admitted")}
}

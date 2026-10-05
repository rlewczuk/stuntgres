package engine

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/sqltype"
)

func TestTypeCoreCatalogDescriptionsAndOwnership(t *testing.T){
 e:=New();view:=e.CatalogRead();for _,id:=range []sqltype.ID{16,20,23,25,705}{d,ok:=view.LookupType(id);if !ok||d.ID()!=id{t.Fatal(id,d,ok)}}
 run(t,e,"CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)")
 op,cleanup,err:=testOperation(e,context.Background());if err!=nil{t.Fatal(err)};defer cleanup()
 desc,err:=e.Describe(op,statement(t,"SELECT * FROM typed"));if err!=nil{t.Fatal(err)}
 for i,id:=range []sqltype.ID{23,20,25,16}{c:=desc.Results[i];if c.Usage.Type.ID()!=id||c.Usage.Typmod!=-1||c.Nullable!=(i!=0)||!c.NullabilityKnown||c.OriginTable==0||c.OriginTable!=desc.Results[0].OriginTable||c.OriginAttribute!=int16(i+1){t.Fatal(c)}}
 run(t,e,"CREATE TABLE repeats(a INT,b TEXT,c INT)")
 p,err:=e.Describe(op,statement(t,"INSERT INTO repeats VALUES($1,$2,$1)"));if err!=nil||p.Parameters.Count!=2||p.Parameters.Types[0].Type.ID()!=23||p.Parameters.Types[1].Type.ID()!=25{t.Fatal(p,err)}
 for _,q:=range []string{"INSERT INTO repeats VALUES($2147483647,'x',1)","INSERT INTO repeats VALUES($2,'x',$2)"}{_,err=e.Describe(op,statement(t,q));state(t,err,"42P18")}
 for _,q:=range []string{"INSERT INTO typed VALUES($1,$1,'x',TRUE)","INSERT INTO repeats VALUES($1,$1,1)"}{_,err=e.Describe(op,statement(t,q));state(t,err,"0A000")}
 run(t,e,"INSERT INTO typed VALUES(1,NULL,NULL,NULL)");old:=run(t,e,"SELECT * FROM typed");values:=make([]sqltype.Value,4);if err=old.Rows.Values(context.Background(),0,values);err!=nil{t.Fatal(err)};for i,v:=range values{if v.Type()!=desc.Results[i].Usage.Type||v.IsNull()!=(i!=0){t.Fatal(v)}}
 ref:=ObjectRef{owner:e.owner,Epoch:view.Epoch(),Address:ObjectAddress{ClassID:TypeClassID,ObjectID:23}};other:=New();if err=other.ValidateObjectRef(other.CatalogRead(),ref);err==nil{t.Fatal("foreign type ref")}
 if err=e.Reset(context.Background());err!=nil{t.Fatal(err)};if err=e.ValidateObjectRef(view,ref);!errors.Is(err,ErrCatalogReplaced){t.Fatal(err)};current,_:=e.CatalogRead().LookupType(23);if current==desc.Results[0].Usage.Type{t.Fatal("reused type generation")};if err=old.Rows.Values(context.Background(),0,values);err!=nil{t.Fatal(err)};if values[0].Type()!=desc.Results[0].Usage.Type{t.Fatal("retained type changed")}
}

func TestTypeCoreCatalogRejectsNoncurrentColumnTypes(t *testing.T) {
 e:=New()
 stale:=testUsage(e,sqltype.Int4)
 if err:=e.Reset(context.Background());err!=nil{t.Fatal(err)}
 for _,usage:=range []sqltype.Usage{{},testUsage(e,sqltype.Unknown),testUsage(New(),sqltype.Int4),stale} {
  base:=e.CatalogRead()
  stage:=e.BeginCatalogStage(base)
  descriptor:=&relationDescriptor{
   address:ObjectAddress{ClassID:RelationClassID,ObjectID:20000},
   namespace:ObjectAddress{ClassID:NamespaceClassID,ObjectID:PublicNamespaceOID},
   name:"invalid",revision:1,
   columns:[]ColumnDescriptor{{Name:"id",Usage:usage}},
   index:map[string]int{"id":0},attributes:[]int32{1},
  }
  if err:=stage.Create(descriptor);err!=nil{t.Fatal(err)}
  if _,err:=e.ValidateCatalogStage(context.Background(),stage);err==nil{t.Fatal("invalid column type accepted",usage)}
  if e.CatalogRead().state!=base.state{t.Fatal("invalid type published")}
 }
}

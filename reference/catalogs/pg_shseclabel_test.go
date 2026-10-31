package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

var shseclabelNames=[]string{"objoid","classoid","provider","label"}
var shseclabelTypes=[]string{"OID","OID","TEXT","TEXT"}

func shseclabelMetadata(t *testing.T,pg bool,columns []*sql.ColumnType) {
 t.Helper()
 if len(columns)!=4{t.Fatal("shared label width",columns)}
 for i,c:=range columns {
  nullable,known:=c.Nullable()
  if c.Name()!=shseclabelNames[i] || c.DatabaseTypeName()!=shseclabelTypes[i] || known==pg || !pg && nullable {t.Fatal(i,c,nullable,known)}
  scan:=reflect.TypeFor[string]();if i<2{scan=reflect.TypeFor[int64]();if pg{scan=reflect.TypeFor[any]()}}
  if c.ScanType()!=scan{t.Fatal("scan type",i,c.ScanType(),scan)}
  if !pg {
   length,known:=c.Length()
   if i<2 && known || i>=2 && (!known || length!=int64(1<<63-1)){t.Fatal("native length",i,length,known)}
   if _,_,ok:=c.DecimalSize();ok{t.Fatal("invented precision",i)}
  }
 }
}
func shseclabelEmpty(pg bool,q string,args ...any) fixture.Step {
 s:=fixture.Select(q,shseclabelNames,shseclabelTypes,nil,args...)
 s.CheckMetadata=func(t *testing.T,rows *sql.Rows){columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};shseclabelMetadata(t,pg,columns)}
 return s
}
func shseclabelCount() fixture.Step{return fixture.Select("SELECT count(*) FROM pg_catalog.pg_shseclabel",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}})}

func TestReferencePGShseclabelReads(t *testing.T) {
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  for _,source:=range []string{"pg_catalog.pg_shseclabel","pg_shseclabel"}{
   for _,suffix:=range []string{""," WHERE FALSE"," LIMIT 0"}{s:=shseclabelEmpty(b.PG,"SELECT * FROM "+source+suffix);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s))}
  }
  for _,s:=range []fixture.Step{shseclabelCount(),fixture.Select("SELECT label,provider,classoid,objoid FROM pg_shseclabel",[]string{"label","provider","classoid","objoid"},[]string{"TEXT","TEXT","OID","OID"},nil)}{fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s))}
  q:="SELECT * FROM pg_shseclabel WHERE objoid=$1"
  stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
  held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
  for _,oid:=range []int64{0,2147483648,4294967295}{s:=shseclabelEmpty(b.PG,q,oid);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s));fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
 })}
}

func shseclabelShape(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor) {
 t.Helper()
 run:=func(s fixture.Step){fixture.OidReferenceRows(t,p.Ctx,b,e,s);fixture.OidReferenceRows(t,p.Ctx,b,e,fixture.Prepared(s))}
 run(fixture.Select("SELECT oid,relname,relnamespace,relowner,reltype,relnatts,relkind,relpersistence,relisshared,relhasindex,reltablespace FROM pg_catalog.pg_class WHERE oid=3592",[]string{"oid","relname","relnamespace","relowner","reltype","relnatts","relkind","relpersistence","relisshared","relhasindex","reltablespace"},[]string{"OID","NAME","OID","OID","OID","INT2","CHAR","CHAR","BOOL","BOOL","OID"},[][]any{{int64(3592),"pg_shseclabel",int64(11),int64(10),int64(4066),int64(4),"r","p",true,true,int64(1664)}}))
 var attrs [][]any
 for i,name:=range shseclabelNames{typ,coll:=int64(26),int64(0);if i>=2{typ,coll=25,950};attrs=append(attrs,[]any{int64(i+1),name,typ,true,int64(-1),int64(0),coll})}
 run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims,attcollation FROM pg_catalog.pg_attribute WHERE attrelid=3592 AND attnum>0 ORDER BY attnum",[]string{"attnum","attname","atttypid","attnotnull","atttypmod","attndims","attcollation"},[]string{"INT2","NAME","OID","BOOL","INT4","INT2","OID"},attrs))
 rows,err:=e.QueryContext(p.Ctx,"SELECT typarray::bigint FROM pg_catalog.pg_type WHERE oid=4066");if err!=nil{t.Fatal(err)}
 var array int64
 if !rows.Next(){rows.Close();t.Fatal("missing shared label row type")};if err:=rows.Scan(&array);err!=nil{rows.Close();t.Fatal(err)};if rows.Next() || rows.Err()!=nil{rows.Close();t.Fatal("duplicate row type")};rows.Close()
 if array<=0 || array==4066 || !b.PG && array!=10093{t.Fatal("shared label array",array)}
 run(fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typowner,typarray FROM pg_catalog.pg_type WHERE oid=4066",[]string{"typname","typtype","typrelid","typnamespace","typowner","typarray"},[]string{"NAME","CHAR","OID","OID","OID","OID"},[][]any{{"pg_shseclabel","c",int64(3592),int64(11),int64(10),array}}))
 run(fixture.Select("SELECT typname,typelem,typnamespace,typowner FROM pg_catalog.pg_type WHERE oid=$1",[]string{"typname","typelem","typnamespace","typowner"},[]string{"NAME","OID","OID","OID"},[][]any{{"_pg_shseclabel",int64(4066),int64(11),int64(10)}},array))
}
func TestReferencePGShseclabelShape(t *testing.T){p:=fixture.NewScenario(t);for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){shseclabelShape(t,p,b,b.DB)})}}

func TestReferencePGShseclabelDiagnostics(t *testing.T) {
 withShseclabelReader(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn){
  for _,e:=range []fixture.Executor{admin,actor}{for _,column:=range []string{"objsubid","oid"}{for _,prepared:=range []bool{false,true}{
   want:=fixture.ControlsReferenceDiagnostic("42703",`column "`+column+`" does not exist`);want.Position="8"
   if column=="objsubid"{want.Hint=`Perhaps you meant to reference the column "pg_shseclabel.objoid".`}
   s:=fixture.ErrorDiagnostic("SELECT "+column+" FROM pg_catalog.pg_shseclabel",want);s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";if prepared{s.ErrorStage="prepare"};fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
  }}}
 })
}

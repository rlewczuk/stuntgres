package enginetests

import (
 "context"
 "encoding/binary"
 "testing"

 "stuntgres/internal/engine"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/sqltype"
)

func TestPageinspectBrinStoredDescriptor(t *testing.T) {
 e:=engine.New()
 view:=e.CatalogRead()
 class,ok:=sqltype.LookupBrinIndexClass("oid_minmax_ops")
 if !ok {t.Fatal("missing released class")}
 class,err:=routine.BindClassSupport(view,class)
 if err!=nil {t.Fatal(err)}
 input,ok:=view.LookupType(sqltype.Int4)
 if !ok {t.Fatal("missing input descriptor")}
 storage,ok:=view.LookupType(sqltype.ID(class.StorageOID()))
 if !ok || storage.ID()!=sqltype.Oid {t.Fatal("missing resolved storage descriptor")}
 column,err:=sqltype.BindBrinStoredColumn(input.Usage(),class,storage)
 if err!=nil {t.Fatal(err)}
 text,_:=view.LookupType(sqltype.Text)
 if _,err:=sqltype.BindBrinStoredColumn(input.Usage(),class,text);err==nil {t.Fatal("foreign storage type accepted")}
 if _,err:=sqltype.BindBrinStoredColumn(input.Usage(),class,nil);err==nil {t.Fatal("missing storage type accepted")}
 // Independent physical bytes distinguish signed input interpretation from
 // the actual opckeytype. No table, table row or native encoder is involved.
 raw:=make([]byte,8192)
 binary.LittleEndian.PutUint16(raw[12:],28)
 binary.LittleEndian.PutUint16(raw[14:],8168)
 binary.LittleEndian.PutUint16(raw[16:],8184)
 binary.LittleEndian.PutUint16(raw[18:],0x2004)
 binary.LittleEndian.PutUint16(raw[8190:],0xf093)
 binary.LittleEndian.PutUint32(raw[24:],8168|1<<15|16<<17)
 raw[8172]=8
 binary.LittleEndian.PutUint32(raw[8176:],0xffffffff)
 binary.LittleEndian.PutUint32(raw[8180:],0xfffffff9)
 page,err:=sqltype.ReadBrinPage(string(raw),0xf093)
 if err!=nil {t.Fatal(err)}
 items,err:=page.Items(context.Background(),[]sqltype.BrinStoredColumn{column},sqltype.OutputEnvironment{})
 if err!=nil || len(items)!=1 || items[0].Value!="{4294967295 .. 4294967289}" {t.Fatal(items,err)}
}

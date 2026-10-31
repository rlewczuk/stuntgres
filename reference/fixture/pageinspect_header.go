package fixture

import (
 "bytes"
 "database/sql"
 "encoding/binary"
 "reflect"
 "testing"
)

var HeaderNames = []string{"lsn","checksum","flags","lower","upper","special","pagesize","version","prune_xid"}
var HeaderTypes = []string{"PG_LSN","INT2","INT2","INT4","INT4","INT4","INT4","INT2","XID"}
var HeaderZero = []any{"0/0",int64(0),int64(0),int64(0),int64(0),int64(0),int64(0),int64(0),"0"}
var HeaderAsymmetricRow = []any{"1234567/89ABCDEF",int64(-292),int64(-32763),int64(65535),int64(32768),int64(65534),int64(65280),int64(254),"4294967295"}

// HeaderAsymmetric assembles REL_18_6 PageHeaderData, not an engine-produced
// page. PageXLogRecPtr stores the high uint32 first, each word little-endian.
func HeaderAsymmetric() []byte {
 p:=make([]byte,8192)
 binary.LittleEndian.PutUint32(p[0:],0x01234567)
 binary.LittleEndian.PutUint32(p[4:],0x89abcdef)
 for i,v:=range []uint16{0xfedc,0x8005,0xffff,0x8000,0xfffe,0xfffe} {binary.LittleEndian.PutUint16(p[8+2*i:],v)}
 binary.LittleEndian.PutUint32(p[20:],0xffffffff)
 return p
}
func HeaderPattern(pattern ...byte) []byte {return bytes.Repeat(pattern,8192/len(pattern))}
func HeaderNullRows() [][]any {return [][]any{make([]any,9)}}

func HeaderCheckColumns(t *testing.T,columns []*sql.ColumnType,names,types []string,pg,view bool) {
 t.Helper()
 if len(columns)!=len(names) {t.Fatal("column count",len(columns),names)}
 for i,c:=range columns {
  if c.Name()!=names[i] || c.DatabaseTypeName()!=types[i] {t.Fatal(i,c.Name(),c.DatabaseTypeName(),names[i],types[i])}
  var scan reflect.Type
  switch types[i] {
  case "INT2":scan=reflect.TypeFor[int64]();if pg {scan=reflect.TypeFor[int16]()}
  case "INT4":scan=reflect.TypeFor[int64]();if pg {scan=reflect.TypeFor[int32]()}
  case "PG_LSN","RECORD":scan=reflect.TypeFor[string]();if pg {scan=reflect.TypeFor[any]()}
  case "XID":scan=reflect.TypeFor[[]byte]();if pg {scan=reflect.TypeFor[any]()}
  default:t.Fatal("unmapped header type",types[i])
  }
  if c.ScanType()!=scan {t.Fatal(i,"ScanType",c.ScanType(),scan)}
  nullable,known:=c.Nullable();want:=view&&!pg
  if nullable!=want || known!=want {t.Fatal(i,"Nullable",nullable,known,"want",want,want)}
  if n,k:=c.Length();n!=0||k {t.Fatal(i,"Length",n,k)}
  if p,s,k:=c.DecimalSize();p!=0||s!=0||k {t.Fatal(i,"DecimalSize",p,s,k)}
 }
}
func HeaderMetadata(s Step,pg,view bool) Step {
 s.CheckMetadata=func(t *testing.T,r *sql.Rows){c,e:=r.ColumnTypes();if e!=nil {t.Fatal(e)};HeaderCheckColumns(t,c,s.Columns,s.Types,pg,view)}
 return s
}

// HeaderDeclaration selects only the modern bytea / bytea,int8 signatures.
// Stock PostgreSQL also carries historical overloads, outside this provider.
func HeaderDeclaration(checksum bool,name,schema,owner string) Step {
 nargs,result,args,source:=int64(1),int64(2249),"17","page_header"
 var allargs,modes any="{17,3220,21,21,23,23,23,23,21,28}","{i,o,o,o,o,o,o,o,o,o}"
 names:="{page,lsn,checksum,flags,lower,upper,special,pagesize,version,prune_xid}"
 if checksum {nargs,result,args,source=2,21,"17 20","page_checksum_1_9";allargs,modes=nil,nil;names="{page,blkno}"}
 return Select("SELECT p.proname::text AS name,n.nspname::text AS schema,r.rolname::text AS owner,p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proargtypes::text AS args,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.prorows AS rows,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.procost AS cost,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='"+name+"' AND n.nspname='"+schema+"' AND p.proargtypes::text='"+args+"'",[]string{"name","schema","owner","nargs","result","args","allargs","modes","names","sets","rows","strict","volatility","parallel","cost","source","binary","language"},[]string{"TEXT","TEXT","TEXT","INT4","INT8","TEXT","TEXT","TEXT","TEXT","BOOL","FLOAT4","BOOL","TEXT","TEXT","FLOAT4","TEXT","TEXT","TEXT"},[][]any{{name,schema,owner,nargs,result,args,allargs,modes,names,false,float64(0),true,"v","s",float64(1),source,"$libdir/pageinspect","c"}})
}

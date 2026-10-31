package fixture

import (
 "database/sql"
 "encoding/binary"
 "math"
 "reflect"
 "strconv"
 "strings"
 "testing"
)

// Hash images are independent REL_18_6 amd64 bufpage/hash/itup layouts.
// No engine decoder or logical index contents participate in their construction.
func HashPage(flags uint16,tuples ...[]byte)[]byte{
 p:=BTPage(0,0,0,tuples...)
 binary.LittleEndian.PutUint32(p[8176:],0xffffffff)
 binary.LittleEndian.PutUint32(p[8180:],17)
 binary.LittleEndian.PutUint32(p[8184:],0x80000000)
 binary.LittleEndian.PutUint16(p[8188:],flags)
 binary.LittleEndian.PutUint16(p[8190:],0xff80)
 return p
}
func HashBucket()[]byte{
 p:=HashPage(2,BTTuple(65538,513,16,255,255,255,255,0,0,0,0),BTTuple(9,7,16,0,0,0,128,0,0,0,0))
 word:=binary.LittleEndian.Uint32(p[28:]);binary.LittleEndian.PutUint32(p[28:],word|3<<15)
 return p
}
func HashMeta()[]byte{
 p:=HashPage(8)
 binary.LittleEndian.PutUint32(p[24:],0x06440640);binary.LittleEndian.PutUint32(p[28:],4)
 binary.LittleEndian.PutUint64(p[32:],math.Float64bits(12.5))
 for i,v:=range []uint16{65535,8192,4096,15}{binary.LittleEndian.PutUint16(p[40+2*i:],v)}
 for i,v:=range []uint32{0xffffffff,0x80000000,17,3,0xffffffff,1,4000000000}{binary.LittleEndian.PutUint32(p[48+4*i:],v)}
 binary.LittleEndian.PutUint32(p[76:],7);binary.LittleEndian.PutUint32(p[464:],0xffffffff)
 binary.LittleEndian.PutUint32(p[468:],9);binary.LittleEndian.PutUint32(p[4560:],0x80000000)
 return p
}
var HashMembers=[]string{"hash_page_type","hash_page_stats","hash_page_items","hash_metapage_info"}
var HashNames=[][]string{{"hash_page_type"},{"live_items","dead_items","page_size","free_size","hasho_prevblkno","hasho_nextblkno","hasho_bucket","hasho_flag","hasho_page_id"},{"itemoffset","ctid","data"},{"magic","version","ntuples","ffactor","bsize","bmsize","bmshift","maxbucket","highmask","lowmask","ovflpoint","firstfree","nmaps","procid","spares","mapp"}}
var HashTypes=[][]string{{"TEXT"},{"INT4","INT4","INT4","INT4","INT8","INT8","INT8","INT4","INT4"},{"INT4","TID","INT8"},{"INT8","INT8","FLOAT8","INT4","INT4","INT4","INT4","INT8","INT8","INT8","INT8","INT8","INT8","OID","_INT8","_INT8"}}
var HashStatsRows=[][]any{{int64(1),int64(1),int64(8192),int64(8108),int64(4294967295),int64(17),int64(2147483648),int64(2),int64(65408)}}
var HashItemsRows=[][]any{{int64(1),"(65538,513)",int64(4294967295)},{int64(2),"(9,7)",int64(2147483648)}}
func HashArrayText(length int,first,last uint32)string{values:=make([]string,length);for i:=range values{values[i]="0"};values[0]=strconv.FormatUint(uint64(first),10);values[length-1]=strconv.FormatUint(uint64(last),10);return "{"+strings.Join(values,",")+"}"}
var HashMetaRows=[][]any{{int64(105121344),int64(4),float64(12.5),int64(65535),int64(8192),int64(4096),int64(15),int64(4294967295),int64(2147483648),int64(17),int64(3),int64(4294967295),int64(1),int64(4000000000),HashArrayText(98,7,4294967295),HashArrayText(1024,9,2147483648)}}
func HashNullRows(i int)[][]any{if i==2{return nil};return [][]any{make([]any,len(HashNames[i]))}}
func HashOrdinary(i int)[]byte{if i==3{return HashMeta()};return HashBucket()}
func HashRows(i int)[][]any{switch i{case 0:return [][]any{{"bucket"}};case 1:return HashStatsRows;case 2:return HashItemsRows;default:return HashMetaRows}}
func HashMetadata(s Step,pg,view bool)Step{
 if pg{rows:=make([][]any,len(s.Rows));for i,row:=range s.Rows{rows[i]=append([]any(nil),row...);for j,v:=range row{if s.Types[j]=="OID"&&v!=nil{if number,ok:=v.(int64);ok{rows[i][j]=strconv.FormatInt(number,10)}}}};s.Rows=rows}
 names,types:=s.Columns,s.Types
 s.CheckMetadata=func(t *testing.T,r *sql.Rows){t.Helper();columns,err:=r.ColumnTypes();if err!=nil{t.Fatal(err)};HashCheckColumns(t,columns,names,types,pg,view)}
 return s
}
func HashCheckColumns(t *testing.T,columns []*sql.ColumnType,names,types []string,pg,view bool){
 t.Helper();if len(columns)!=len(names){t.Fatal("hash metadata width",len(columns),len(names))}
  for i,c:=range columns{
   if c.Name()!=names[i]||c.DatabaseTypeName()!=types[i]{t.Fatal("hash declaration",i,c.Name(),c.DatabaseTypeName(),names[i],types[i])}
   scan:=reflect.TypeFor[int64]();switch types[i]{case "TEXT":scan=reflect.TypeFor[string]();case "FLOAT8":scan=reflect.TypeFor[float64]();case "INT4":if pg{scan=reflect.TypeFor[int32]()};case "TID","_INT8","RECORD":scan=reflect.TypeFor[string]();if pg{scan=reflect.TypeFor[any]()};case "OID":if pg{scan=reflect.TypeFor[any]()}}
   if c.ScanType()!=scan{t.Fatal("hash scan",i,c.ScanType(),scan)}
   nullable,known:=c.Nullable();want:=view&&!pg;if nullable!=want||known!=want{t.Fatal("hash nullability",i,nullable,known,want)}
   length,ok:=c.Length();if types[i]=="TEXT"{if length!=math.MaxInt64||!ok{t.Fatal("hash text length",length,ok)}}else if length!=0||ok{t.Fatal("hash length",i,length,ok)}
   precision,scale,ok:=c.DecimalSize();if precision!=0||scale!=0||ok{t.Fatal("hash decimal size",i,precision,scale,ok)}
  }
}
func HashDeclaration(i int,name,schema,owner string)Step{
 outs:=[]string{"","{17,23,23,23,23,20,20,20,23,23}","{17,23,27,20}","{17,20,20,701,23,23,23,23,20,20,20,20,20,20,26,1016,1016}"}
 modes:=[]string{"","{i,o,o,o,o,o,o,o,o,o}","{i,o,o,o}","{i,o,o,o,o,o,o,o,o,o,o,o,o,o,o,o,o}"}
 names:="{page}";var all,m any
 result:=int64(25);set:=false;rows:=float64(0)
 if i!=0{result=2249;all=outs[i];m=modes[i];names="{page,"+strings.Join(HashNames[i],",")+"}"};if i==2{set=true;rows=1000}
 return Select(`SELECT p.prorettype::bigint AS result,p.proretset AS isset,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.proargtypes::text AS inputs,p.proallargtypes::text AS alltypes,p.proargmodes::text AS modes,p.proargnames::text AS names,p.prosrc AS source,p.probin AS library,p.procost,p.prorows,r.rolname::text AS owner FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='`+schema+`' AND p.proname='`+name+`' AND p.proargtypes::text='17'`,[]string{"result","isset","strict","volatility","parallel","inputs","alltypes","modes","names","source","library","procost","prorows","owner"},[]string{"INT8","BOOL","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","FLOAT4","FLOAT4","TEXT"},[][]any{{result,set,true,"v","s","17",all,m,names,HashMembers[i],"$libdir/pageinspect",float64(1),rows,owner}})
}

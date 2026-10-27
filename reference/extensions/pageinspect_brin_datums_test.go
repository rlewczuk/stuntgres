package reference_test

import (
 "database/sql"
 "encoding/binary"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func brinLE16(v uint16)[]byte{p:=make([]byte,2);binary.LittleEndian.PutUint16(p,v);return p}
func brinLE32(v uint32)[]byte{p:=make([]byte,4);binary.LittleEndian.PutUint32(p,v);return p}
func brinLE64(v uint64)[]byte{p:=make([]byte,8);binary.LittleEndian.PutUint64(p,v);return p}
func brinWords(words ...uint16)[]byte{var p []byte;for _,v:=range words{p=append(p,brinLE16(v)...)};return p}
func brinCompressed(method uint32,payload []byte,size uint32)[]byte{p:=make([]byte,8);binary.LittleEndian.PutUint32(p,uint32(8+len(payload))<<2|2);binary.LittleEndian.PutUint32(p[4:],size|method<<30);return append(p,payload...)}

// Native layouts are from REL_18_6 pg_type.dat, numeric.c, inet.h, varbit.h,
// timestamp.h, date.h, uuid.h, mac.h and itemptr.h, not wire send/receive.
func TestReferencePageinspectBrinDatums(t *testing.T){
 name:=make([]byte,64);copy(name,"independent")
 cases:=[]struct{name,typ,class string;datum []byte;align int;short bool;want string}{
  {"int2","smallint","",brinLE16(0x8000),2,false,"-32768"},
  {"int4","integer","",brinLE32(0x80000000),4,false,"-2147483648"},
  {"int8","bigint","",brinLE64(0x8000000000000000),8,false,"-9223372036854775808"},
  {"oid","oid","",brinLE32(0xffffffff),4,false,"4294967295"},
  {"int4_oid_class","integer","oid_minmax_ops",brinLE32(0xffffffff),4,false,"4294967295"},
  {"regclass","regclass","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regtype","regtype","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regclass_oid_storage","regclass","oid_minmax_ops",brinLE32(1259),4,false,"1259"},
  {"regtype_oid_storage","regtype","oid_minmax_ops",brinLE32(23),4,false,"23"},
  {"regrole","regrole","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regproc","regproc","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regprocedure","regprocedure","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regoper","regoper","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regoperator","regoperator","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regnamespace","regnamespace","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regcollation","regcollation","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regconfig","regconfig","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"regdictionary","regdictionary","oid_minmax_ops",brinLE32(4294967000),4,false,"4294967000"},
  {"tid","tid","",[]byte{1,128,2,0,0,0},2,false,"(2147549186,0)"},
  {"char","\"char\"","",[]byte{'Q'},1,false,"Q"},
  {"name","name","",name,1,false,"independent"},
  {"float4","real","",brinLE32(0x80000000),4,false,"-0"},
  {"float8","double precision","",brinLE64(0x7ff0000000000000),8,false,"Infinity"},
  {"float8_nan","double precision","",brinLE64(0x7ff8000000000001),8,false,"NaN"},
  {"text","text","",brinShort([]byte("é{ .. }")),4,true,"é{ .. }"},
  {"varchar","varchar(20)","",brinShort([]byte("arbitrary")),4,true,"arbitrary"},
  {"bpchar","character(7)","",brinShort([]byte("a   ")),4,true,"a   "},
  {"text_bpchar_class","text","bpchar_minmax_ops",brinShort([]byte("a   ")),4,true,"a   "},
  {"bytea","bytea","",brinShort([]byte{0,255,92}),4,true,"\\x00ff5c"},
  {"bit","bit(11)","",brinLong(append(brinLE32(11),0xa5,0xa0)),4,false,"10100101101"},
  {"varbit","bit varying","",brinShort(append(brinLE32(3),0xa0)),4,true,"101"},
  {"numeric_short","numeric","",brinShort(brinWords(0xa201,1,2345,6789)),4,true,"-12345.6789"},
  {"numeric_long","numeric","",brinLong(brinWords(0x4002,1,1,2345,6700)),4,false,"-12345.67"},
  {"numeric_nan","numeric","",brinShort(brinWords(0xc000)),4,true,"NaN"},
  {"numeric_infinity","numeric","",brinShort(brinWords(0xd000)),4,true,"Infinity"},
  {"date","date","",brinLE32(0),4,false,"2000-01-01"},
  {"date_infinity","date","",brinLE32(0x7fffffff),4,false,"infinity"},
  {"time","time","",brinLE64(1234567),8,false,"00:00:01.234567"},
  {"timetz","time with time zone","",append(brinLE64(1234567),brinLE32(18000)...),8,false,"00:00:01.234567-05"},
  {"timestamp","timestamp","",brinLE64(1234567),8,false,"2000-01-01 00:00:01.234567"},
  {"timestamptz","timestamp with time zone","",brinLE64(1234567),8,false,"2000-01-01 00:00:01.234567+00"},
  {"interval","interval","",append(append(brinLE64(1234567),brinLE32(2)...),brinLE32(3)...),8,false,"3 mons 2 days 00:00:01.234567"},
  {"inet4","inet","inet_minmax_ops",brinShort([]byte{2,24,192,0,2,7}),4,true,"192.0.2.7/24"},
  {"cidr","cidr","inet_minmax_ops",brinShort([]byte{2,24,192,0,2,0}),4,true,"192.0.2.0/24"},
  {"inet6","inet","inet_minmax_ops",brinLong([]byte{3,64,0x20,1,0x0d,0xb8,0,0,0,0,0,0,0,0,0,0,0,1}),4,false,"2001:db8::1/64"},
  {"macaddr","macaddr","",[]byte{0,1,2,0xfe,0xff,7},4,false,"00:01:02:fe:ff:07"},
  {"macaddr8","macaddr8","",[]byte{0x80,1,2,3,4,5,6,7},4,false,"80:01:02:03:04:05:06:07"},
  {"uuid","uuid","",[]byte{0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15},1,false,"00010203-0405-0607-0809-0a0b0c0d0e0f"},
  {"long_text","text","",brinLong([]byte(strings.Repeat("xyé",256))),4,false,strings.Repeat("xyé",256)},
  // pg_lzcompress.c: one literal A and an overlapping offset-1 length-99 tag.
  {"pglz","text","",brinCompressed(0,[]byte{2,'A',15,1,81},100),4,false,strings.Repeat("A",100)},
  // LZ4 raw block: 1 literal, offset-1 match94, final five literals.
  {"lz4","text","",brinCompressed(1,[]byte{0x1f,'A',1,0,75,0x50,'A','A','A','A','A'},100),4,false,strings.Repeat("A",100)},
 }
 for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{"SET TIME ZONE 'UTC'","SET datestyle TO 'ISO, MDY'","SET intervalstyle TO 'postgres'","SET bytea_output TO 'hex'","CREATE TABLE pi_data(n "+tc.typ+")","CREATE INDEX pi_idx ON pi_data USING brin(n "+tc.class+")"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  page:=brinItems(brinTuple(0,nil,brinPair(tc.datum,tc.align,tc.short)))
  brinBoth(t,p,b,c,brinItemStep(page,[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,"{"+tc.want+" .. "+tc.want+"}"}}))
 })})}
 t.Run("mixed_alignment",func(t *testing.T){brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{"CREATE TABLE pi_data(a text,b bigint,c numeric,d text)","CREATE INDEX pi_idx ON pi_data USING brin(a,b,c,d)"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  var data []byte
  add:=func(value []byte,align int,short bool){if !short{for (8+len(data))%align!=0{data=append(data,0)}};data=append(data,value...)}
  add(brinShort([]byte("x")),4,true);add(brinShort([]byte("yz")),4,true)
  add(brinLE64(7),8,false);add(brinLE64(42),8,false)
  add(brinShort(brinWords(0x8000,7)),4,true);add(brinShort(brinWords(0x8000,42)),4,true)
  add(brinLong([]byte("normal")),4,false);add(brinLong([]byte("header")),4,false)
  rows:=make([][]any,4);for i,want:=range []string{"{x .. yz}","{7 .. 42}","{7 .. 42}","{normal .. header}"}{rows[i]=[]any{int64(1),int64(2147549186),int64(i+1),false,false,false,false,want}}
  brinBoth(t,p,b,c,brinItemStep(brinItems(brinTuple(0,nil,data)),rows))
 })})
 t.Run("domain",func(t *testing.T){brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{"CREATE DOMAIN pi_domain AS integer CHECK (VALUE > 1000)","CREATE TABLE pi_data(n pi_domain)","CREATE INDEX pi_idx ON pi_data USING brin(n)"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  // Stored output does not reapply a domain's input constraints to raw bytes.
  brinBoth(t,p,b,c,brinItemStep(brinItems(brinInt4(-7,42)),[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,"{-7 .. 42}"}}))
 })})
 t.Run("descriptor_lifetime",func(t *testing.T){brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,q:=range []string{"CREATE TABLE pi_first(n integer)","CREATE INDEX pi_first_idx ON pi_first USING brin(n)","CREATE TABLE pi_second(n text)","CREATE INDEX pi_second_idx ON pi_second USING brin(n)"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
  q:="SELECT * FROM brin_part.brin_page_items($1,$2) ORDER BY itemoffset,attnum"
  st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
  for _,tc:=range []struct{index string;page []byte;want string}{
   {"pi_first_idx",brinItems(brinInt4(-7,42)),"{-7 .. 42}"},
   {"pi_second_idx",brinItems(brinTuple(0,nil,append(brinShort([]byte("not")),brinShort([]byte("rows"))...))),"{not .. rows}"},
   {"pi_first_idx",brinItems(brinInt4(42,-7)),"{42 .. -7}"},
  }{
   s:=fixture.Select(q,brinItemNames,brinItemTypes,[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,tc.want}},tc.page,tc.index)
   brinBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  }
 })})
}

package reference_test

import (
 "database/sql"
 "encoding/binary"
 "math"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectHashTypesAndStats(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{flags uint16;name string}{{0,"unused"},{1,"overflow"},{2,"bucket"},{4,"bitmap"},{8,"metapage"},{0x8002,"bucket"}}{
  raw:=fixture.HashPage(tc.flags);if tc.flags==8{raw=fixture.HashMeta()};hashBoth(t,p,b,c,hashStep(0,raw,[][]any{{tc.name}}))
 }
 hashBoth(t,p,b,c,hashStep(0,make([]byte,8192),[][]any{{"unused"}}))
 poisoned:=fixture.HashPage(15);binary.LittleEndian.PutUint16(poisoned[14:],0);binary.LittleEndian.PutUint16(poisoned[16:],65535);hashBoth(t,p,b,c,hashStep(0,poisoned,[][]any{{"unused"}}))
 empty:=[][]any{{int64(0),int64(0),int64(8192),int64(8148),int64(4294967295),int64(17),int64(2147483648),int64(2),int64(65408)}}
 hashBoth(t,p,b,c,hashStep(1,fixture.HashPage(2),empty));hashBoth(t,p,b,c,hashStep(1,fixture.HashBucket(),fixture.HashStatsRows))
 one:=fixture.HashPage(2,fixture.BTTuple(65538,513,16,255,255,255,255,0,0,0,0));row:=append([]any(nil),empty[0]...);row[0]=int64(1);row[3]=int64(8128);hashBoth(t,p,b,c,hashStep(1,one,[][]any{row}))
 for _,low:=range []byte{0,255}{raw:=fixture.HashPage(2);raw[18]=low;hashBoth(t,p,b,c,hashStep(1,raw,empty))}
 relocated:=fixture.HashPage(2);copy(relocated[4080:],relocated[8176:]);binary.LittleEndian.PutUint16(relocated[16:],4080);binary.LittleEndian.PutUint16(relocated[18:],0x1004);row=append([]any(nil),empty[0]...);row[2]=int64(4096);hashBoth(t,p,b,c,hashStep(1,relocated,[][]any{row}))
 raw:=fixture.HashPage(0x8002);row=append([]any(nil),empty[0]...);row[7]=int64(32770);hashBoth(t,p,b,c,hashStep(1,raw,[][]any{row}))
 raw=fixture.HashPage(2);binary.LittleEndian.PutUint16(raw[12:],40);for i:=range 4{binary.LittleEndian.PutUint32(raw[24+4*i:],uint32(i)<<15)};row=append([]any(nil),empty[0]...);row[0]=int64(3);row[1]=int64(1);row[3]=int64(8132);hashBoth(t,p,b,c,hashStep(1,raw,[][]any{row}))
 for _,lower:=range []uint16{0,23,8172,8176,8190}{raw:=fixture.HashPage(2);binary.LittleEndian.PutUint16(raw[12:],lower);row=append([]any(nil),empty[0]...);count:=int64(0);if lower>=24{count=int64((lower-24)/4)};row[0]=count;if lower==8190{row[0]=count-1;row[1]=int64(1)};free:=int64(8176)-int64(lower)-4;if free<0{free=0};row[3]=free;hashBoth(t,p,b,c,hashStep(1,raw,[][]any{row}))}
})}
func TestReferencePageinspectHashItems(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hashBoth(t,p,b,c,hashStep(2,fixture.HashBucket(),fixture.HashItemsRows))
 for _,flags:=range []uint16{1,2}{hashBoth(t,p,b,c,hashStep(2,fixture.HashPage(flags),nil))}
 raw:=fixture.HashBucket();first:=binary.LittleEndian.Uint32(raw[24:]);second:=binary.LittleEndian.Uint32(raw[28:]);binary.LittleEndian.PutUint32(raw[24:],second);binary.LittleEndian.PutUint32(raw[28:],first)
 hashBoth(t,p,b,c,hashStep(2,raw,[][]any{{int64(1),"(9,7)",int64(2147483648)},{int64(2),"(65538,513)",int64(4294967295)}}))
 for _,info:=range []uint16{16,0x4010,0x2010,0x8018}{for _,value:=range []uint32{0,0x80000000,0xffffffff}{
  payload:=make([]byte,16);at:=0;if info&0x8000!=0{binary.LittleEndian.PutUint32(payload,123);at=8};binary.LittleEndian.PutUint32(payload[at:],value)
  raw:=fixture.HashPage(2,fixture.BTTuple(65538,513,info,payload...));want:=[][]any{{int64(1),"(65538,513)",int64(value)}}
  hashBoth(t,p,b,c,hashStep(2,raw,want))
  for _,flags:=range []uint32{0,2,3}{word:=binary.LittleEndian.Uint32(raw[24:]);binary.LittleEndian.PutUint32(raw[24:],word&0x7fff|flags<<15);hashBoth(t,p,b,c,hashStep(2,raw,want))}
 }}
})}
func TestReferencePageinspectHashMetapage(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hashBoth(t,p,b,c,hashStep(3,fixture.HashMeta(),fixture.HashMetaRows))
 hashBoth(t,p,b,c,fixture.Select("SELECT array_lower(spares,1) AS sl,array_upper(spares,1) AS su,array_lower(mapp,1) AS ml,array_upper(mapp,1) AS mu FROM hash_part.hash_metapage_info($1)",[]string{"sl","su","ml","mu"},[]string{"INT4","INT4","INT4","INT4"},[][]any{{int64(1),int64(98),int64(1),int64(1024)}},fixture.HashMeta()))
 st,err:=c.PrepareContext(p.Ctx,hashQuery(3));if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:hashQuery(3)}
 for _,value:=range []float64{19.25,math.Inf(1),math.Inf(-1),12.5}{raw:=fixture.HashMeta();binary.LittleEndian.PutUint64(raw[32:],math.Float64bits(value));row:=append([]any(nil),fixture.HashMetaRows[0]...);row[2]=value;s:=hashStep(3,raw,[][]any{row});hashBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.HashMetadata(s,b.PG,false))}
 nan:=fixture.HashMeta();binary.LittleEndian.PutUint64(nan[32:],0x7ff8000000000001)
 hashBoth(t,p,b,c,fixture.Select("SELECT ntuples::text AS tuples FROM hash_part.hash_metapage_info($1)",[]string{"tuples"},[]string{"TEXT"},[][]any{{"NaN"}},nan))
 floatSQL:="SELECT ntuples FROM hash_part.hash_metapage_info($1)";floatStatement,err:=c.PrepareContext(p.Ctx,floatSQL);if err!=nil{t.Fatal(err)};defer floatStatement.Close()
 for _,prepared:=range []bool{false,true}{var value float64;if prepared{err=floatStatement.QueryRowContext(p.Ctx,nan).Scan(&value)}else{err=c.QueryRowContext(p.Ctx,floatSQL,nan).Scan(&value)};if err!=nil||!math.IsNaN(value){t.Fatal("native float NaN transport",value,err)}}
})}

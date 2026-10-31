package reference_test

import (
 "database/sql"
 "encoding/binary"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectHeaderFields(t *testing.T) {headerCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 cases:=[]struct{raw []byte;row []any;record string}{
  {make([]byte,8192),fixture.HeaderZero,"(0/0,0,0,0,0,0,0,0,0)"},
  {fixture.HeaderPattern(255),[]any{"FFFFFFFF/FFFFFFFF",int64(-1),int64(-1),int64(65535),int64(65535),int64(65535),int64(65280),int64(255),"4294967295"},"(FFFFFFFF/FFFFFFFF,-1,-1,65535,65535,65535,65280,255,4294967295)"},
  {fixture.HeaderPattern(1),[]any{"1010101/1010101",int64(257),int64(257),int64(257),int64(257),int64(257),int64(256),int64(1),"16843009"},"(1010101/1010101,257,257,257,257,257,256,1,16843009)"},
  {fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow,"(1234567/89ABCDEF,-292,-32763,65535,32768,65534,65280,254,4294967295)"},
 }
 source,scalar:=[]fixture.Step{},[]fixture.Step{}
 for _,tc:=range cases {
  source=append(source,headerStep(headerSource,tc.raw,tc.row))
  scalar=append(scalar,fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{tc.record}},tc.raw))
 }
 // Full-size arbitrary metadata is decoded, never treated as a page validity
 // check. Independently vary layout version, page size, flags and offsets.
 for _,tc:=range []struct{offset int;word uint16;field int;want int64}{
  {18,0xff00,7,0},{18,0xffff,7,255},{18,0x01fe,6,256},{10,0x0001,2,1},{12,0,3,0},{14,1,4,1},{16,0,5,0},
 } {
  raw:=fixture.HeaderAsymmetric();binary.LittleEndian.PutUint16(raw[tc.offset:],tc.word)
  row:=append([]any(nil),fixture.HeaderAsymmetricRow...);row[tc.field]=tc.want
  source=append(source,headerStep(headerSource,raw,row))
 }
 headerHeld(t,p,b,c,headerSource,source);headerHeld(t,p,b,c,headerScalar,scalar)
 for _,q:=range []string{"CREATE TABLE header_bytes(raw bytea)","CREATE TABLE header_empty(id integer)"} {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 raw:=fixture.HeaderAsymmetric();fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO header_bytes VALUES($1)",1,raw))
 headerBoth(t,p,b,c,fixture.Select("SELECT header_part.page_header(raw),raw FROM header_bytes",[]string{"page_header","raw"},[]string{"RECORD","BYTEA"},[][]any{{cases[3].record,string(raw)}}))
})}

func TestReferencePageinspectChecksumVectors(t *testing.T) {headerCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 // Independently published REL_18_6 contrib/pageinspect/expected/checksum.out.
 vectors:=[]struct{pattern []byte;want [3]int64}{
  {[]byte{1},[3]int64{1175,1225,1139}}, {[]byte{4},[3]int64{28338,28352,28438}},
  {[]byte{255},[3]int64{3612,3598,3648}}, {[]byte{0xab,0xcd},[3]int64{-30781,-30795,-30881}},
  {[]byte{0xe6,0xd6},[3]int64{-16269,-16251,-16305}}, {[]byte{0x4a,0x5e},[3]int64{-27377,-27391,-27349}},
 }
 for _,query:=range []string{checksumScalar,checksumSource} {
  steps:=[]fixture.Step{}
  for _,v:=range vectors {for j,block:=range []int64{0,50,100} {steps=append(steps,checksumStep(query,fixture.HeaderPattern(v.pattern...),block,v.want[j]))}}
  stored:=fixture.HeaderPattern(1);stored[8],stored[9]=0xdc,0xfe
  steps=append(steps,checksumStep(query,stored,int64(0),int64(1175)))
  // Independent REL_18_6 checksum_impl.h source arithmetic, confirmed against
  // the pinned PostgreSQL 18.6 oracle; no runtime-generated expectations.
  body:=fixture.HeaderPattern(1);body[100]^=1
  flags:=fixture.HeaderPattern(1);flags[10]^=1
  steps=append(steps,checksumStep(query,body,int64(0),int64(3816)),checksumStep(query,flags,int64(0),int64(-13768)),checksumStep(query,fixture.HeaderPattern(1),int64(2147483648),int64(-31594)),checksumStep(query,fixture.HeaderPattern(1),int64(4294967294),int64(-1175)))
  headerHeld(t,p,b,c,query,steps)
 }
 raw:=fixture.HeaderPattern(1);raw[8],raw[9]=0xdc,0xfe
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE checksum_bytes(raw bytea)",0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO checksum_bytes VALUES($1)",1,raw))
 headerBoth(t,p,b,c,fixture.Select("SELECT header_part.page_checksum(raw,0),raw FROM checksum_bytes",[]string{"page_checksum","raw"},[]string{"INT2","BYTEA"},[][]any{{int64(1175),string(raw)}}))
 row:=[]any{"1010101/1010101",int64(-292),int64(257),int64(257),int64(257),int64(257),int64(256),int64(1),"16843009"}
 headerBoth(t,p,b,c,headerStep(headerSource,raw,row))
 headerBoth(t,p,b,c,checksumStep("SELECT header_part.page_checksum($1,$2::integer)",fixture.HeaderPattern(1),100,int64(1139)))
 // A PageIsNew image has upper==0, regardless of all its other bytes.
 raw=fixture.HeaderPattern(255);raw[14],raw[15]=0,0
 headerBoth(t,p,b,c,checksumStep(checksumScalar,raw,int64(0),nil))
 for _,n:=range []int{0,3,8191,8193} {headerBoth(t,p,b,c,headerError(checksumScalar,"22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n),make([]byte,n),int64(0)))}
})}

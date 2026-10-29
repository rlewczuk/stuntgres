package reference_test

import (
 "database/sql"
 "encoding/binary"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferencePageinspectGINMetapage(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 check:=func(raw []byte,rows [][]any) {ginBoth(t,p,b,c,ginStep(0,raw,rows))}
 check(fixture.GINMeta(),fixture.GINMetaRows);check(ginRelocated(fixture.GINMeta()),fixture.GINMetaRows)
 for _,version:=range []int32{0,1,2,-1,2147483647,-2147483648} {
  raw:=fixture.GINMeta();binary.LittleEndian.PutUint32(raw[72:],uint32(version))
  row:=append([]any(nil),fixture.GINMetaRows[0]...);row[9]=int64(version);check(raw,[][]any{row})
 }
 for _,tc:=range []struct{free uint32;tuples,entries uint64;wantFree,wantTuples,wantEntries int64}{
  {0x80000000,0x8000000000000000,0x7fffffffffffffff,-2147483648,-9223372036854775808,9223372036854775807},
  {0x7fffffff,0x7fffffffffffffff,0x8000000000000000,2147483647,9223372036854775807,-9223372036854775808},
 } {
  raw:=fixture.GINMeta();for _,off:=range []int{24,28,36,48,52,56} {binary.LittleEndian.PutUint32(raw[off:],0xffffffff)}
  binary.LittleEndian.PutUint32(raw[32:],tc.free);binary.LittleEndian.PutUint64(raw[40:],tc.tuples);binary.LittleEndian.PutUint64(raw[64:],tc.entries)
  check(raw,[][]any{{int64(4294967295),int64(4294967295),tc.wantFree,int64(4294967295),tc.wantTuples,int64(4294967295),int64(4294967295),int64(4294967295),tc.wantEntries,int64(2)}})
 }
 for _,low:=range []byte{0,255} {
  raw:=fixture.GINMeta();raw[18]=low
  // LSN, checksum, header flags, prune XID and free space are not verified.
  for _,off:=range []int{0,1,2,3,4,5,6,7,8,9,10,11,20,21,22,23,100,101} {raw[off]=0xa5}
  check(raw,fixture.GINMetaRows)
 }
 check(nil,fixture.GINNullRows(0));check(make([]byte,8192),fixture.GINNullRows(0))
 poisoned:=fixture.GINMeta();binary.LittleEndian.PutUint16(poisoned[14:],0);binary.LittleEndian.PutUint16(poisoned[16:],65535);binary.LittleEndian.PutUint16(poisoned[18:],0xffff)
 check(poisoned,fixture.GINNullRows(0))
})}

func TestReferencePageinspectGINOpaque(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 for _,tc:=range []struct{flags uint16;want string}{
  {0,"{}"},{1,"{data}"},{2,"{leaf}"},{4,"{deleted}"},{8,"{meta}"},{16,"{list}"},{32,"{list_fullrow}"},{64,"{incomplete_split}"},{128,"{compressed}"},{0x100,"{100}"},{0x8103,"{data,leaf,8100}"},{0xffff,fixture.GINFlags},
 } {
  s:=ginStep(1,fixture.GINPage(tc.flags),[][]any{{int64(4294967295),int64(65535),tc.want}});ginBoth(t,p,b,c,s)
 }
 ginBoth(t,p,b,c,ginStep(1,ginRelocated(fixture.GINPage(0xffff)),fixture.GINOpaqueRows))
 zero:=fixture.GINPage(0);clear(zero[8184:8192]);ginBoth(t,p,b,c,ginStep(1,zero,[][]any{{int64(0),int64(0),"{}"}}))
 for _,low:=range []byte{0,255} {raw:=fixture.GINPage(0xffff);raw[18]=low;ginBoth(t,p,b,c,ginStep(1,raw,fixture.GINOpaqueRows))}
 for _,raw:=range [][]byte{nil,make([]byte,8192)} {ginBoth(t,p,b,c,ginStep(1,raw,fixture.GINNullRows(1)))}
 for _,tc:=range []struct{raw []byte;row []any}{
  {fixture.GINPage(0xffff),[]any{"data","ff00",int64(1),int64(9)}},
  {fixture.GINPage(0),[]any{nil,nil,nil,nil}},
 } {ginBoth(t,p,b,c,fixture.Select("SELECT flags[1] AS first,flags[9] AS last,array_lower(flags,1) AS lo,array_upper(flags,1) AS hi FROM gin_part.gin_page_opaque_info($1)",[]string{"first","last","lo","hi"},[]string{"TEXT","TEXT","INT4","INT4"},[][]any{tc.row},tc.raw))}
})}

func TestReferencePageinspectGINLeaf(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 ginBoth(t,p,b,c,ginStep(2,fixture.GINPosting(),fixture.GINLeafRows))
 ginBoth(t,p,b,c,ginStep(2,ginRelocated(fixture.GINPosting()),fixture.GINLeafRows))
 for _,tc:=range []struct{raw []byte;rows [][]any}{
  {fixture.GINLeaf(fixture.GINSegment{0,1,[]byte{0x80,0x80,0x80,0x80,0x80,0x80,1}}),[][]any{{"(0,1)",int64(7),`{"(0,1)","(2147483648,1)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{4294967294,2047,nil}),[][]any{{"(4294967294,2047)",int64(0),`{"(4294967294,2047)"}`}}},
  // Bounded numerical carries narrow the decoded block to uint32; they do
  // not authorize an invented 43-bit accumulator validation gate.
  {fixture.GINLeaf(fixture.GINSegment{4294967294,2047,[]byte{0x82,0x10}}),[][]any{{"(4294967294,2047)",int64(2),`{"(4294967294,2047)","(0,1)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{0xffffffff,2047,[]byte{1}}),[][]any{{"(4294967295,2047)",int64(1),`{"(4294967295,2047)","(0,0)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{8,41,[]byte{0}}),[][]any{{"(8,41)",int64(1),`{"(8,41)","(8,41)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{0,65535,[]byte{1}}),[][]any{{"(0,65535)",int64(1),`{"(0,65535)","(32,0)"}`}}},
  // REL_18_6 decode_varbyte consumes at most seven octets. The seventh is
  // used in full, including its assertion-only continuation bit. A following
  // octet starts a new delta, rather than extending the preceding integer.
  {fixture.GINLeaf(fixture.GINSegment{0,1,[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x7f}}),[][]any{{"(0,1)",int64(7),`{"(0,1)","(2147483648,1)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{0,1,[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80}}),[][]any{{"(0,1)",int64(7),`{"(0,1)","(0,1)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{0,1,[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80,1}}),[][]any{{"(0,1)",int64(8),`{"(0,1)","(0,1)","(0,2)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{8,41,[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,2}}),[][]any{{"(8,41)",int64(10),`{"(8,41)","(8,41)","(24,41)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{8,41,[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0}}),[][]any{{"(8,41)",int64(11),`{"(8,41)","(8,41)","(8,41)"}`}}},
  {fixture.GINLeaf(fixture.GINSegment{0,0,nil}),[][]any{{"(0,0)",int64(0),`{"(0,0)"}`}}},
  {fixture.GINLeaf(),nil},{nil,nil},{make([]byte,8192),fixture.GINNullRows(2)},
 } {ginBoth(t,p,b,c,ginStep(2,tc.raw,tc.rows))}
 raw:=fixture.GINPosting();for _,off:=range []int{0,1,8,9,10,11,20,21,22,23,24,25,26,27,28,29,30,31,8184,8185,8186,8187,8188,8189} {raw[off]=0xdd};raw[18]=255
 ginBoth(t,p,b,c,ginStep(2,raw,fixture.GINLeafRows))
 duplicate:=ginStep(2,fixture.GINLeaf(fixture.GINSegment{8,41,nil},fixture.GINSegment{8,41,nil}),[][]any{{"(8,41)",int64(0),`{"(8,41)"}`},{"(8,41)",int64(0),`{"(8,41)"}`}});duplicate.Bag=true;ginBoth(t,p,b,c,duplicate)
 ginBoth(t,p,b,c,fixture.Select("SELECT first_tid,tids[1] AS first,tids[2] AS second,array_lower(tids,1) AS lo,array_upper(tids,1) AS hi FROM gin_part.gin_leafpage_items($1) ORDER BY first_tid",[]string{"first_tid","first","second","lo","hi"},[]string{"TID","TID","TID","INT4","INT4"},[][]any{{"(8,41)","(8,41)","(8,43)",int64(1),int64(4)},{"(65538,513)","(65538,513)","(65539,1)",int64(1),int64(2)},{"(90000,7)","(90000,7)",nil,int64(1),int64(1)}},fixture.GINPosting()))
})}

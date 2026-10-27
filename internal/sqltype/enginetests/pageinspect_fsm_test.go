package enginetests

import (
 "encoding/binary"
 "errors"
 "fmt"
 "reflect"
 "strings"
 "testing"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

const fsmUnitSparseText="0: 255\n1: 128\n4094: 127\n4095: 1\n8163: 254\nfp_next_slot: 16909060\n"
func fsmUnitPage()[]byte{p:=make([]byte,8192);binary.LittleEndian.PutUint16(p[12:],24);binary.LittleEndian.PutUint16(p[14:],8192);binary.LittleEndian.PutUint16(p[16:],8192);binary.LittleEndian.PutUint16(p[18:],0x2004);return p}
func fsmUnitSparse()[]byte{p:=fsmUnitPage();binary.LittleEndian.PutUint32(p[24:],0x01020304);for i,v:=range map[int]byte{0:255,1:128,4094:127,4095:1,8163:254}{p[28+i]=v};return p}
func fsmUnitAssert(t *testing.T,p []byte,want string,newPage bool){t.Helper();before:=append([]byte(nil),p...);got,isNew,err:=sqltype.ReadFSMPageContents(string(p));if err!=nil || got!=want || isNew!=newPage{t.Fatalf("contents %q new=%v err=%v",got,isNew,err)};if !reflect.DeepEqual(p,before){t.Fatal("input mutated")};clear(p);if got!=want{t.Fatal("returned text borrowed caller bytes")}}
func TestPageinspectFSMContents(t *testing.T){
 fsmUnitAssert(t,fsmUnitSparse(),fsmUnitSparseText,false)
 p:=fsmUnitPage();var want strings.Builder;for i:=range 8164{p[28+i]=1;fmt.Fprintf(&want,"%d: 1\n",i)};want.WriteString("fp_next_slot: 0\n");fsmUnitAssert(t,p,want.String(),false)
 for _,tc:=range []struct{pointer uint32;text string}{{0,"fp_next_slot: 0\n"},{0x80000000,"fp_next_slot: -2147483648\n"},{0x7fffffff,"fp_next_slot: 2147483647\n"},{0xffffffff,"fp_next_slot: -1\n"}}{p:=fsmUnitPage();binary.LittleEndian.PutUint32(p[24:],tc.pointer);fsmUnitAssert(t,p,tc.text,false)}
 // Each call reads its current input rather than retaining a prior image.
 p=fsmUnitSparse();fsmUnitAssert(t,p,fsmUnitSparseText,false);fsmUnitAssert(t,p,"",true)
}
func TestPageinspectFSMBoundaries(t *testing.T){
 fsmUnitAssert(t,make([]byte,8192),"",true)
 p:=fsmUnitSparse();binary.LittleEndian.PutUint16(p[14:],0);fsmUnitAssert(t,p,"",true)
 for _,mutate:=range []func([]byte){
  func(p []byte){binary.LittleEndian.PutUint16(p[14:],1)},
  func(p []byte){binary.LittleEndian.PutUint16(p[18:],0xffff)},
  func(p []byte){binary.LittleEndian.PutUint16(p[10:],0xffff)},
  func(p []byte){binary.LittleEndian.PutUint16(p[16:],1)},
  func(p []byte){binary.LittleEndian.PutUint16(p[12:],0xffff);binary.LittleEndian.PutUint16(p[10:],0xffff);binary.LittleEndian.PutUint16(p[16:],0xffff);binary.LittleEndian.PutUint16(p[18:],0)},
 }{p:=fsmUnitSparse();mutate(p);fsmUnitAssert(t,p,fsmUnitSparseText,false)}
}
func TestPageinspectFSMErrors(t *testing.T){for _,n:=range []int{0,3,8191,8193}{contents,newPage,err:=sqltype.ReadFSMPageContents(string(make([]byte,n)));var got *sqlstate.Error;want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:"invalid page size",Detail:fmt.Sprintf("Expected 8192 bytes, got %d.",n)};if !errors.As(err,&got) || !reflect.DeepEqual(*got,want) || contents!="" || newPage{t.Fatal(n,contents,newPage,err)}}}

package reference_test

import (
 "database/sql"
 "encoding/binary"
 "encoding/hex"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectFSMContents(t *testing.T){fsmCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 cases:=[]struct{raw []byte;want any}{{fixture.FSMSparse(),fixture.FSMSparseText},{fixture.FSMPage(0),"fp_next_slot: 0\n"},{fixture.FSMPage(0x80000000),"fp_next_slot: -2147483648\n"},{fixture.FSMPage(0x7fffffff),"fp_next_slot: 2147483647\n"},{fixture.FSMPage(0xffffffff),"fp_next_slot: -1\n"},{nil,nil},{make([]byte,8192),nil}}
 dense:=fixture.FSMPage(0);var expected strings.Builder;for i:=range 8164{dense[28+i]=1;fmt.Fprintf(&expected,"%d: 1\n",i)};expected.WriteString("fp_next_slot: 0\n");cases=append(cases,struct{raw []byte;want any}{dense,expected.String()})
 newSparse:=fixture.FSMSparse();binary.LittleEndian.PutUint16(newSparse[14:],0);cases=append(cases,struct{raw []byte;want any}{newSparse,nil})
 for _,mutate:=range []func([]byte){
  func(p []byte){binary.LittleEndian.PutUint16(p[14:],1)},
  func(p []byte){binary.LittleEndian.PutUint16(p[18:],0xffff)},
  func(p []byte){binary.LittleEndian.PutUint16(p[10:],0xffff)},
  func(p []byte){binary.LittleEndian.PutUint16(p[16:],1)},
  func(p []byte){binary.LittleEndian.PutUint16(p[10:],0xffff);binary.LittleEndian.PutUint16(p[12:],0xffff);binary.LittleEndian.PutUint16(p[16:],0xffff);binary.LittleEndian.PutUint16(p[18:],0)},
 }{raw:=fixture.FSMSparse();mutate(raw);cases=append(cases,struct{raw []byte;want any}{raw,fixture.FSMSparseText})}
 for _,query:=range []string{fsmScalar,fsmSource}{
  for _,tc:=range cases{fsmBoth(t,p,b,c,fsmStep(query,tc.raw,tc.want))}
  st,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:query}
  for _,tc:=range []struct{raw []byte;want any}{{fixture.FSMSparse(),fixture.FSMSparseText},{fixture.FSMPage(0xffffffff),"fp_next_slot: -1\n"},{nil,nil},{make([]byte,8192),nil},{fixture.FSMSparse(),fixture.FSMSparseText}}{fixture.RunStep(t,p.Ctx,held,b.Recorder,fsmStep(query,tc.raw,tc.want))};if err:=st.Close();err!=nil{t.Fatal(err)}
 }
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET search_path=fsm_part,public",0))
 for _,query:=range []string{"SELECT fsm_page_contents(page => $1)","SELECT * FROM fsm_page_contents(page => $1)","SELECT fsm_part.fsm_page_contents(page => $1)","SELECT * FROM fsm_part.fsm_page_contents(page => $1)"}{fsmBoth(t,p,b,c,fsmStep(query,fixture.FSMSparse(),fixture.FSMSparseText))}
 for _,query:=range []string{"SELECT fsm_part.fsm_page_contents($1) AS contents","SELECT * FROM fsm_part.fsm_page_contents($1) AS contents"}{s:=fsmStep(query,fixture.FSMSparse(),fixture.FSMSparseText);s.Columns=[]string{"contents"};fsmBoth(t,p,b,c,s)}
 literal:="'\\x"+hex.EncodeToString(fixture.FSMSparse())+"'::bytea"
 for _,query:=range []string{"SELECT fsm_part.fsm_page_contents("+literal+")","SELECT * FROM fsm_part.fsm_page_contents("+literal+")"}{s:=fsmStep(query,nil,fixture.FSMSparseText);s.Args=nil;fsmBoth(t,p,b,c,s)}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("RESET search_path",0))
})}

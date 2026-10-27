package reference_test

import (
 "database/sql"
 "encoding/binary"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

// Independent REL_18_6 ginblock.h/ginpostinglist.c amd64 images. No engine
// decoder, physical relation, or embedded row participates in their construction.
func ginPage(flags uint16) []byte {
 p := make([]byte,8192)
 binary.LittleEndian.PutUint16(p[12:],32)
 binary.LittleEndian.PutUint16(p[14:],8184)
 binary.LittleEndian.PutUint16(p[16:],8184)
 binary.LittleEndian.PutUint16(p[18:],0x2004)
 binary.LittleEndian.PutUint32(p[8184:],0xffffffff)
 binary.LittleEndian.PutUint16(p[8188:],65535)
 binary.LittleEndian.PutUint16(p[8190:],flags)
 return p
}
func ginMeta() []byte {
 p:=ginPage(8)
 for _,f:=range []struct{offset int;value uint32}{{24,0xffffffff},{28,17},{32,0xffffffff},{36,19},{48,29},{52,31},{56,37},{72,2}} {binary.LittleEndian.PutUint32(p[f.offset:],f.value)}
 binary.LittleEndian.PutUint64(p[40:],23)
 binary.LittleEndian.PutUint64(p[64:],41)
 copy(p[60:64],[]byte{0xde,0xad,0xbe,0xef})
 return p
}
func ginTID(p []byte,block uint32,offset uint16) {
 binary.LittleEndian.PutUint16(p,uint16(block>>16))
 binary.LittleEndian.PutUint16(p[2:],uint16(block))
 binary.LittleEndian.PutUint16(p[4:],offset)
}
type ginSegment struct {block uint32; offset uint16; deltas []byte}
func ginLeaf(segments ...ginSegment) []byte {
 p:=ginPage(0x83);end:=32
 for _,s:=range segments {
  ginTID(p[end:],s.block,s.offset)
  binary.LittleEndian.PutUint16(p[end+6:],uint16(len(s.deltas)))
  copy(p[end+8:],s.deltas)
  end+=8+len(s.deltas)
  if len(s.deltas)%2!=0 {p[end]=0xee;end++}
 }
 binary.LittleEndian.PutUint16(p[12:],uint16(end))
 return p
}
func ginPosting() []byte {return ginLeaf(ginSegment{8,41,[]byte{2,1,127}},ginSegment{65538,513,[]byte{0x80,0x0c}},ginSegment{90000,7,nil})}
func ginRelocated(p []byte) []byte {
 copy(p[4088:4096],p[8184:8192]);clear(p[8184:8192])
 binary.LittleEndian.PutUint16(p[16:],4088)
 binary.LittleEndian.PutUint16(p[18:],0x1004)
 return p
}
var ginMembers=[]string{"gin_metapage_info","gin_page_opaque_info","gin_leafpage_items"}
var ginNames=[][]string{
 {"pending_head","pending_tail","tail_free_size","n_pending_pages","n_pending_tuples","n_total_pages","n_entry_pages","n_data_pages","n_entries","version"},
 {"rightlink","maxoff","flags"},
 {"first_tid","nbytes","tids"},
}
var ginTypes=[][]string{
 {"INT8","INT8","INT4","INT8","INT8","INT8","INT8","INT8","INT8","INT4"},
 {"INT8","INT4","_TEXT"},
 {"TID","INT2","_TID"},
}
var ginMetaRows=[][]any{{int64(4294967295),int64(17),int64(-1),int64(19),int64(23),int64(29),int64(31),int64(37),int64(41),int64(2)}}
const ginFlags="{data,leaf,deleted,meta,list,list_fullrow,incomplete_split,compressed,ff00}"
var ginOpaqueRows=[][]any{{int64(4294967295),int64(65535),ginFlags}}
var ginLeafRows=[][]any{
 {"(8,41)",int64(3),`{"(8,41)","(8,43)","(8,44)","(8,171)"}`},
 {"(65538,513)",int64(2),`{"(65538,513)","(65539,1)"}`},
 {"(90000,7)",int64(0),`{"(90000,7)"}`},
}
func ginOrdinary(i int) []byte {switch i {case 0:return ginMeta();case 1:return ginPage(0xffff);default:return ginPosting()}}
func ginOrdinaryRows(i int) [][]any {return [][][]any{ginMetaRows,ginOpaqueRows,ginLeafRows}[i]}
func ginNullRows(i int) [][]any {return [][]any{make([]any,len(ginNames[i]))}}
func ginQuery(i int) string {q:="SELECT * FROM gin_part."+ginMembers[i]+"($1)";if i==2 {q+=" ORDER BY first_tid"};return q}
func ginStep(i int,p []byte,rows [][]any) fixture.Step {return fixture.Select(ginQuery(i),ginNames[i],ginTypes[i],rows,p)}
func ginBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step) {
 t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
}
func ginMissing(query,signature string,args ...any) fixture.Step {
 return fixture.ImplicitError(query,fixture.Diagnostic{Code:"42883",Message:"function "+signature+" does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(strings.Index(query,"gin_part.")+1)},args...)
}
func ginCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {t.Helper();ginCaseSetup(t,true,body)}
func ginCaseSetup(t *testing.T,install bool,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)) {
 t.Helper();fixture.SchemaReferenceCleanup(t,"gin_part","gin_moved")
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
  c,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer c.Close()
  if b.PG {
   clean:=func() {for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS gin_part CASCADE","DROP SCHEMA IF EXISTS gin_moved CASCADE"} {if _,err:=c.ExecContext(p.Ctx,q);err!=nil {t.Error(q,err)}}}
   clean();defer clean()
  }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SCHEMA gin_part",0))
  if install {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE EXTENSION pageinspect SCHEMA gin_part",0))}
  body(t,p,b,c)
 })}
}

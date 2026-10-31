package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

// Check raw transport before fixture normalization, plus descriptor stability
// after EOF/Close. The same held statement sees populated, NULL and empty rows.
func headerMetadataLifetime(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,query string,steps []fixture.Step) {
 t.Helper();st,e:=c.PrepareContext(p.Ctx,query);if e!=nil {t.Fatal(e)};defer st.Close()
 for _,s:=range steps {
  headerBoth(t,p,b,c,fixture.HeaderMetadata(s,b.PG,false))
  rows,e:=st.QueryContext(p.Ctx,s.Args...);if e!=nil {t.Fatal(e)}
  columns,e:=rows.ColumnTypes();if e!=nil {rows.Close();t.Fatal(e)}
  fixture.HeaderCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
  count:=0
  for rows.Next() {
   values:=make([]any,len(s.Columns));dest:=make([]any,len(values));for i:=range values {dest[i]=&values[i]}
   if e:=rows.Scan(dest...);e!=nil {rows.Close();t.Fatal(e)}
   if count>=len(s.Rows) {rows.Close();t.Fatal("unexpected metadata row",values)}
   for i,v:=range values {
    if v!=nil {switch s.Types[i] {
    case "PG_LSN","RECORD":if b.PG {if _,ok:=v.([]byte);!ok {t.Fatalf("raw PG %s: %T",s.Types[i],v)}} else if _,ok:=v.(string);!ok {t.Fatalf("raw native %s: %T",s.Types[i],v)}
    case "XID":if _,ok:=v.([]byte);!ok {t.Fatalf("raw XID: %T",v)}
    case "INT2","INT4":if _,ok:=v.(int64);!ok {t.Fatalf("raw integer: %T",v)}
    }}
    if got:=fixture.Normalize(v);!reflect.DeepEqual(got,s.Rows[count][i]) {t.Fatal("raw metadata value",count,i,got,s.Rows[count][i])}
   };count++
  }
  if e:=rows.Err();e!=nil {rows.Close();t.Fatal(e)};if count!=len(s.Rows) {rows.Close();t.Fatal("metadata cardinality",count,len(s.Rows))}
  fixture.HeaderCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
  if e:=rows.Close();e!=nil {t.Fatal(e)}
  fixture.HeaderCheckColumns(t,columns,s.Columns,s.Types,b.PG,false)
 }
}
func headerIdentities() fixture.Step {
 return fixture.Select("SELECT h.oid::bigint>0 AS header_valid,c.oid::bigint>0 AS checksum_valid,h.oid<>c.oid AS distinct_ids,h.proowner=c.proowner AS same_owner,dh.refobjid=e.oid AS header_member,dc.refobjid=e.oid AS checksum_member FROM pg_proc h JOIN pg_namespace n ON n.oid=h.pronamespace JOIN pg_proc c ON c.pronamespace=n.oid JOIN pg_depend dh ON dh.objid=h.oid JOIN pg_depend dc ON dc.objid=c.oid JOIN pg_extension e ON e.oid=dh.refobjid WHERE n.nspname='header_part' AND h.proname='page_header' AND h.proargtypes::text='17' AND c.proname='page_checksum' AND c.proargtypes::text='17 20' AND dh.classid=1255 AND dh.refclassid=3079 AND dh.deptype='e' AND dc.classid=1255 AND dc.refclassid=3079 AND dc.deptype='e' AND e.extname='pageinspect'",[]string{"header_valid","checksum_valid","distinct_ids","same_owner","header_member","checksum_member"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true}})
}
func TestReferencePageinspectHeaderMetadata(t *testing.T) {headerCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 owner:="postgres";if b.PG {owner="stuntgres"}
 headerBoth(t,p,b,c,fixture.HeaderDeclaration(false,"page_header","header_part",owner))
 headerBoth(t,p,b,c,fixture.HeaderDeclaration(true,"page_checksum","header_part",owner))
 headerBoth(t,p,b,c,headerIdentities())
 source:=[]fixture.Step{headerStep(headerSource,fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow),headerStep(headerSource,nil,make([]any,9)),headerStep(headerSource,make([]byte,8192),fixture.HeaderZero)}
 scalar:=[]fixture.Step{fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{"(1234567/89ABCDEF,-292,-32763,65535,32768,65534,65280,254,4294967295)"}},fixture.HeaderAsymmetric()),fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{nil}},nil)}
 headerMetadataLifetime(t,p,b,c,headerSource,source);headerMetadataLifetime(t,p,b,c,headerScalar,scalar)
 for _,q:=range []string{checksumScalar,checksumSource} {headerMetadataLifetime(t,p,b,c,q,[]fixture.Step{checksumStep(q,fixture.HeaderPattern(0xab,0xcd),int64(0),int64(-30781)),checksumStep(q,nil,int64(0),nil),checksumStep(q,make([]byte,8192),int64(0),nil)})}
 for _,s:=range []fixture.Step{source[0],source[1],scalar[0],scalar[1],checksumStep(checksumSource,fixture.HeaderPattern(1),int64(0),int64(1175)),checksumStep(checksumScalar,nil,int64(0),nil)} {
  s.Query+=" WHERE false";s.Rows=nil;headerMetadataLifetime(t,p,b,c,s.Query,[]fixture.Step{s})
 }
 s:=headerStep("SELECT h.lsn AS log,h.prune_xid AS xid FROM header_part.page_header($1) AS h",fixture.HeaderAsymmetric(),[]any{"1234567/89ABCDEF","4294967295"});s.Columns=[]string{"log","xid"};s.Types=[]string{"PG_LSN","XID"};headerMetadataLifetime(t,p,b,c,s.Query,[]fixture.Step{s})
})}

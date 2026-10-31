package reference_test

import (
 "database/sql"
 "encoding/hex"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferencePageinspectHashDeclarations(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 principal:="postgres";if b.PG{principal="stuntgres"}
 for i,name:=range fixture.HashMembers{fixture.BRINBoth(t,p,b,c,fixture.HashDeclaration(i,name,"hash_part",principal))}
 fixture.BRINBoth(t,p,b,c,fixture.Select(`SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='hash_part' AND p.proname='hash_bitmap_info' AND p.proargtypes::text='17'`,[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
 fixture.BRINBoth(t,p,b,c,fixture.Select(`SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='hash_part' AND p.proname IN ('hash_page_type','hash_page_stats','hash_page_items','hash_metapage_info') AND l.lanname='c' AND p.prokind='f' AND d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pageinspect'`,[]string{"n"},[]string{"INT8"},[][]any{{int64(4)}}))
})}
func TestReferencePageinspectHashBinding(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE TABLE hash_binding(id integer,page bytea)",0));run(fixture.Exec("INSERT INTO hash_binding VALUES(1,$1)",1,fixture.HashBucket()))
 for i,name:=range fixture.HashMembers{
  raw:=fixture.HashOrdinary(i);rows:=fixture.HashRows(i)
  for _,argument:=range []string{"page=>$1::bytea","'\\x"+hex.EncodeToString(raw)+"'::bytea"}{
   q:="SELECT * FROM hash_part."+name+"("+argument+")";if i==2{q+=" ORDER BY itemoffset"};args:=[]any(nil);if argument=="page=>$1::bytea"{args=[]any{raw}}
   hashBoth(t,p,b,c,fixture.Select(q,fixture.HashNames[i],fixture.HashTypes[i],rows,args...))
  }
  query:="SELECT hash_part."+name+"($1::text)";bad:=hashMissing(query,"hash_part."+name+"(text)","abc");fixture.BRINBoth(t,p,b,c,bad)
  query="SELECT hash_part."+name+"()";fixture.BRINBoth(t,p,b,c,hashMissing(query,"hash_part."+name+"()"))
  query="SELECT hash_part."+name+"(wrong=>NULL::bytea)";fixture.BRINBoth(t,p,b,c,hashMissing(query,"hash_part."+name+"(wrong => bytea)"))
 }
 hashBoth(t,p,b,c,fixture.Select("SELECT hash_part.hash_page_type(page) AS kind FROM hash_binding",[]string{"kind"},[]string{"TEXT"},[][]any{{"bucket"}}))
 hashBoth(t,p,b,c,fixture.Select("SELECT s.* FROM hash_binding CROSS JOIN LATERAL hash_part.hash_page_stats(page) AS s",fixture.HashNames[1],fixture.HashTypes[1],fixture.HashStatsRows))
 hashBoth(t,p,b,c,fixture.Select("SELECT i.ordinal,i.location,i.key FROM hash_binding CROSS JOIN LATERAL hash_part.hash_page_items(page) AS i(ordinal,location,key) ORDER BY ordinal",[]string{"ordinal","location","key"},fixture.HashTypes[2],fixture.HashItemsRows))
 hashBoth(t,p,b,c,fixture.Select("SELECT (hash_part.hash_page_stats($1)).*",fixture.HashNames[1],fixture.HashTypes[1],fixture.HashStatsRows,fixture.HashBucket()))
 target:=fixture.Select("SELECT hash_part.hash_page_items($1)",[]string{"hash_page_items"},[]string{"RECORD"},[][]any{{`(1,"(65538,513)",4294967295)`},{`(2,"(9,7)",2147483648)`}},fixture.HashBucket());target.Bag=true;hashBoth(t,p,b,c,target)
 hashBoth(t,p,b,c,fixture.Select("SELECT (hash_part.hash_metapage_info($1)).*",fixture.HashNames[3],fixture.HashTypes[3],fixture.HashMetaRows,fixture.HashMeta()))
 hashBoth(t,p,b,c,fixture.Select("SELECT hash_part.hash_page_stats($1) AS stats",[]string{"stats"},[]string{"RECORD"},[][]any{{"(1,1,8192,8108,4294967295,17,2147483648,2,65408)"}},fixture.HashBucket()))
})}

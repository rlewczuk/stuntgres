package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func hstoreKeysSeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper()
 hstoreInstall(t,p,b,c)
 hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_keys(id integer,v hstore)",0))
 hstoreRun(t,p,b,c,fixture.Exec(`INSERT INTO hs_keys VALUES (1,'a=>NULL,b=>2'),(2,''),(3,NULL),(4,'a=>"NULL"'),(5,'c=>3')`,5))
}

func hstoreKeysVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 q:=`SELECT id,v ? 'a' AS one,v ?& ARRAY['a'] AS all_keys,v ?| ARRAY['b','c'] AS any_key,v @> 'a=>NULL' AS contains,v <@ 'a=>NULL,b=>2' AS contained FROM hs_keys ORDER BY id`
 s:=fixture.Select(q,[]string{"id","one","all_keys","any_key","contains","contained"},[]string{"INT4","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{int64(1),true,true,true,true,true},{int64(2),false,false,false,false,true},{int64(3),nil,nil,nil,nil,nil},{int64(4),true,true,false,false,false},{int64(5),false,false,true,false,false}})
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 for _,tc:=range []struct{predicate string;ids []int64}{{`v ? 'a'`,[]int64{1,4}},{`v ?& ARRAY['a']`,[]int64{1,4}},{`v ?| ARRAY['b','c']`,[]int64{1,5}},{`v @> 'a=>NULL'`,[]int64{1}},{`v <@ 'a=>NULL,b=>2'`,[]int64{1,2}}} {
  rows:=make([][]any,len(tc.ids));for i,id:=range tc.ids {rows[i]=[]any{id}}
  hstoreRun(t,p,b,c,fixture.Select("SELECT id FROM hs_keys WHERE "+tc.predicate+" ORDER BY id",[]string{"id"},[]string{"INT4"},rows))
 }
}

func TestReferenceHstoreKeysValues(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreKeysSeed(t,p,b,c);hstoreKeysVerify(t,p,b,c)
  for _,tc:=range []struct{expression string;want any}{
   {`'a=>NULL'::hstore ? 'a'`,true},{`''::hstore ? 'a'`,false},{`'a=>1'::hstore ? NULL::text`,nil},{`NULL::hstore ? 'a'`,nil},
   {`'a=>NULL,b=>2'::hstore ?& ARRAY['a','b']`,true},{`'a=>1'::hstore ?& ARRAY['a','missing']`,false},
   {`''::hstore ?& '{}'::text[]`,true},{`''::hstore ?| '{}'::text[]`,false},{`''::hstore ?& ARRAY[NULL::text]`,true},{`''::hstore ?| ARRAY[NULL::text]`,false},
   {`'a=>NULL'::hstore ?& ARRAY['a',NULL,'a']`,true},{`'a=>NULL'::hstore ?| ARRAY['missing',NULL,'a']`,true},{`'a=>NULL'::hstore ?& ARRAY['a',NULL,'missing']`,false},
   {`'a=>1'::hstore ?& NULL::text[]`,nil},{`NULL::hstore ?& '{}'::text[]`,nil},{`NULL::hstore ?| '{}'::text[]`,nil},
   {`'a=>NULL,b=>2'::hstore ?& '[0:1][3:4]={{a,b},{a,NULL}}'::text[]`,true},{`'a=>1,b=>2'::hstore ?| '[0:0][3:4]={{missing,b}}'::text[]`,true},
   {`'a=>NULL'::hstore @> 'a=>NULL'`,true},{`'a=>NULL'::hstore @> 'a=>"NULL"'`,false},{`'a=>"NULL"'::hstore @> 'a=>NULL'`,false},{`'b=>2'::hstore @> 'b=>3'`,false},
   {`'a=>1'::hstore @> ''`,true},{`''::hstore @> ''`,true},{`''::hstore @> 'a=>NULL'`,false},{`'a=>NULL'::hstore <@ 'a=>NULL,b=>2'`,true},{`'a=>NULL,b=>2'::hstore <@ 'a=>NULL'`,false},
   {`NULL::hstore @> ''::hstore`,nil},{`''::hstore <@ NULL::hstore`,nil},
   {`exist('a=>NULL','a')`,true},{`exists_all('a=>NULL',ARRAY['a',NULL])`,true},{`exists_any('a=>NULL',ARRAY['missing'])`,false},{`hs_contains('a=>NULL','a=>NULL')`,true},{`hs_contained('','a=>NULL')`,true},
   {`hstore('','') ? ''`,true},{`hstore('雪','β') ? '雪'`,true},{`hstore('a','1') ? 'A'`,false},{`hstore($$a"b\c,o'$$,'v') ? $$a"b\c,o'$$`,true},
  } {s:=fixture.Select("SELECT "+tc.expression+" AS v",[]string{"v"},[]string{"BOOL"},[][]any{{tc.want}});hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))}
  q:=`SELECT akeys(v) AS k,avals(v) AS a FROM hs_keys WHERE id=1`
  rows,err:=c.QueryContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer rows.Close()
  if !rows.Next(){t.Fatal("missing pair arrays")};var keys,values string;if err:=rows.Scan(&keys,&values);err!=nil{t.Fatal(err)}
  // Pair positions are aligned, but neither SQL nor the manual promises insertion order.
  if !((keys=="{a,b}"&&values=="{NULL,2}")||(keys=="{b,a}"&&values=="{2,NULL}")){t.Fatalf("unaligned pairs %q %q",keys,values)}
  if rows.Next()||rows.Err()!=nil{t.Fatal("unexpected pair rows",rows.Err())}
 })
}

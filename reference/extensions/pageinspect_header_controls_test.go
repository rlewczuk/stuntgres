package reference_test

import (
 "database/sql"
 "reflect"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func headerView() fixture.Step {return fixture.Select("SELECT * FROM header_view",fixture.HeaderNames,fixture.HeaderTypes,[][]any{fixture.HeaderAsymmetricRow})}
func checksumView() fixture.Step {return fixture.Select("SELECT checksum FROM checksum_view",[]string{"checksum"},[]string{"INT2"},[][]any{{int64(-30781)}})}

func headerControls(t *testing.T,smoke bool) {
 fixture.RoleReferenceCleanup(t,[]string{"header_owner","header_reader"})
 headerCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{"CREATE ROLE header_owner SUPERUSER","CREATE SCHEMA header_moved","CREATE TABLE header_images(id integer,raw bytea,checkraw bytea,block bigint)"} {run(fixture.Exec(q,0))}
  run(fixture.Exec("INSERT INTO header_images VALUES(1,$1,$2,0)",1,fixture.HeaderAsymmetric(),fixture.HeaderPattern(0xab,0xcd)))
  for _,q:=range []string{"CREATE VIEW header_view AS SELECT (header_part.page_header(raw)).* FROM header_images","CREATE VIEW checksum_view AS SELECT header_part.page_checksum(checkraw,block) AS checksum FROM header_images"} {run(fixture.Exec(q,0))}
  views:=[]fixture.Step{headerView(),checksumView()};held:=make([]fixture.HeldStatement,2)
  for i,s:=range views {st,e:=c.PrepareContext(p.Ctx,s.Query);if e!=nil {t.Fatal(e)};defer st.Close();held[i]=fixture.HeldStatement{Executor:c,Statement:st,SQL:s.Query};fixture.RunStep(t,p.Ctx,held[i],b.Recorder,fixture.HeaderMetadata(s,b.PG,true))}
  headerHeld(t,p,b,c,headerSource,[]fixture.Step{headerStep(headerSource,fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow),headerStep(headerSource,make([]byte,8192),fixture.HeaderZero),headerStep(headerSource,nil,make([]any,9))})
  headerHeld(t,p,b,c,headerScalar,[]fixture.Step{fixture.Select(headerScalar,[]string{"page_header"},[]string{"RECORD"},[][]any{{"(1234567/89ABCDEF,-292,-32763,65535,32768,65534,65280,254,4294967295)"}},fixture.HeaderAsymmetric())})
  headerHeld(t,p,b,c,checksumScalar,[]fixture.Step{checksumStep(checksumScalar,fixture.HeaderPattern(0xab,0xcd),int64(0),int64(-30781)),checksumStep(checksumScalar,fixture.HeaderPattern(0xab,0xcd),int64(50),int64(-30795)),checksumStep(checksumScalar,make([]byte,8192),int64(0),nil),checksumStep(checksumScalar,nil,int64(0),nil)})
  headerBoth(t,p,b,c,checksumStep(checksumSource,nil,int64(0),nil))
  if smoke {headerBoth(t,p,b,c,headerError(headerScalar,"22023","invalid page size","Expected 8192 bytes, got 3.",[]byte{1,2,3}));headerAuthority(t,p,b,c)} else {run(fixture.Exec("CREATE ROLE header_reader",0))}
  for _,q:=range []string{"GRANT USAGE ON SCHEMA public,header_part,header_moved TO header_reader","GRANT SELECT ON header_view,checksum_view TO header_reader"} {run(fixture.Exec(q,0))}
  checkHeld:=func(row []any,sum any){h:=headerView();h.Rows=[][]any{row};s:=checksumView();s.Rows=[][]any{{sum}};for i,v:=range []fixture.Step{h,s} {fixture.RunStep(t,p.Ctx,held[i],b.Recorder,fixture.HeaderMetadata(v,b.PG,true))}}
  changeInputs:=func(){
   run(fixture.Exec("UPDATE header_images SET raw=$1,block=50",1,make([]byte,8192)));checkHeld(fixture.HeaderZero,int64(-30795))
   run(fixture.Exec("UPDATE header_images SET raw=NULL,checkraw=NULL,block=NULL",1));checkHeld(make([]any,9),nil)
   run(fixture.Exec("UPDATE header_images SET raw=$1,checkraw=$2,block=0",1,fixture.HeaderAsymmetric(),fixture.HeaderPattern(0xab,0xcd)));checkHeld(fixture.HeaderAsymmetricRow,int64(-30781))
  }
  changeInputs()
  ids:=make([]int64,2)
  for i,tc:=range []struct{name,signature,moved string}{{"page_header","bytea","decoded"},{"page_checksum","bytea,bigint","checked"}} {
   q:="SELECT 'header_part."+tc.name+"("+tc.signature+")'::regprocedure::bigint"
   if e:=c.QueryRowContext(p.Ctx,q).Scan(&ids[i]);e!=nil||ids[i]<=0 {t.Fatal("live provider identity",ids[i],e)}
   for _,q:=range []string{"ALTER FUNCTION header_part."+tc.name+"("+tc.signature+") OWNER TO header_owner","ALTER FUNCTION header_part."+tc.name+"("+tc.signature+") RENAME TO "+tc.moved,"ALTER FUNCTION header_part."+tc.moved+"("+tc.signature+") SET SCHEMA header_moved"} {run(fixture.Exec(q,0))}
   headerBoth(t,p,b,c,fixture.Select("SELECT 'header_moved."+tc.moved+"("+tc.signature+")'::regprocedure::bigint AS id",[]string{"id"},[]string{"INT8"},[][]any{{ids[i]}}))
  }
  check:=func(conn *sql.Conn,r *fixture.Recorder,pg bool){
   execute:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,r,s);fixture.RunStep(t,p.Ctx,conn,r,fixture.Prepared(s))}
   execute(fixture.HeaderDeclaration(false,"decoded","header_moved","header_owner"));execute(fixture.HeaderDeclaration(true,"checked","header_moved","header_owner"))
   for _,s:=range views {execute(fixture.HeaderMetadata(s,pg,true));s.Query+=" WHERE false";s.Rows=nil;execute(fixture.HeaderMetadata(s,pg,true))}
   execute(fixture.Select("SELECT raw,checkraw,block FROM header_images ORDER BY id",[]string{"raw","checkraw","block"},[]string{"BYTEA","BYTEA","INT8"},[][]any{{string(fixture.HeaderAsymmetric()),string(fixture.HeaderPattern(0xab,0xcd)),int64(0)}}))
   execute(fixture.HeaderMetadata(headerStep("SELECT * FROM header_moved.decoded(page=>$1)",fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow),pg,false))
   s:=checksumStep("SELECT header_moved.checked(blkno=>$2,page=>$1)",fixture.HeaderPattern(0xab,0xcd),int64(50),int64(-30795));s.Columns=[]string{"checked"};execute(fixture.HeaderMetadata(s,pg,false))
   principal:="postgres";if pg {principal="stuntgres"}
   execute(fixture.Select("SELECT n.nspname::text AS target,r.rolname::text AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pageinspect'",[]string{"target","owner"},[]string{"TEXT","TEXT"},[][]any{{"header_part",principal}}))
  }
  checkOriginal:=func(){check(c,b.Recorder,b.PG);checkHeld(fixture.HeaderAsymmetricRow,int64(-30781))}
  checkOriginal();changeInputs();checkOriginal()
  // A retained statement must check the current caller, not the captured owner.
  run(fixture.Exec("SET SESSION AUTHORIZATION header_reader",0))
  for i,s:=range views {denied:=headerError(s.Query,"42501","must be superuser to use raw page functions","");fixture.RunStep(t,p.Ctx,held[i],b.Recorder,denied)}
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  run(fixture.Exec("UPDATE header_images SET raw=NULL,checkraw=NULL",1))
  run(fixture.Exec("SET SESSION AUTHORIZATION header_reader",0));checkHeld(make([]any,9),nil);run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  run(fixture.Exec("UPDATE header_images SET raw=$1,checkraw=$2",1,fixture.HeaderAsymmetric(),fixture.HeaderPattern(0xab,0xcd)));checkOriginal()
  if b.PG {return}
  dump:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  other,destination,r:=pgcryptoControlDestination(t,p,"header_restore")
  for _,q:=range []string{"CREATE ROLE header_owner SUPERUSER","CREATE ROLE header_reader"} {if _,e:=other.ExecContext(p.Ctx,q);e!=nil {t.Fatal(e)}}
  if e:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));e!=nil {t.Fatal(e)};check(destination,r,false)
  if got:=fixture.ExtensionDump(t,p.Ctx,other);got!=dump {t.Fatal("header symbolic redump changed")}
  // Detached eager Rows retain old decoded values and descriptors after Reset.
  retained:=make([]*sql.Rows,2);descriptors:=make([][]*sql.ColumnType,2)
  for i,s:=range views {rows,e:=c.QueryContext(p.Ctx,s.Query);if e!=nil {t.Fatal(e)};retained[i]=rows;defer rows.Close();descriptors[i],e=rows.ColumnTypes();if e!=nil {t.Fatal(e)}}
  run(fixture.Exec("UPDATE header_images SET raw=$1,block=50",1,make([]byte,8192)));checkHeld(fixture.HeaderZero,int64(-30795))
  if e:=stuntgres.Reset(p.Ctx,p.Native);e!=nil {t.Fatal(e)}
  for _,q:=range []string{"SELECT header_moved.decoded($1)","SELECT header_moved.checked($1,0)"} {rows,e:=c.QueryContext(p.Ctx,q,fixture.HeaderAsymmetric());if e==nil {rows.Close();t.Fatal("reset kept provider",q)}}
  for i,rows:=range retained {
   s:=views[i];fixture.HeaderCheckColumns(t,descriptors[i],s.Columns,s.Types,false,true)
   if !rows.Next() {t.Fatal("retained row absent",rows.Err())};v:=make([]any,len(s.Columns));d:=make([]any,len(v));for j:=range v {d[j]=&v[j]};if e:=rows.Scan(d...);e!=nil {t.Fatal(e)}
   for j:=range v {if !reflect.DeepEqual(fixture.Normalize(v[j]),s.Rows[0][j]) {t.Fatal("retained row changed",v,s.Rows)}}
   if rows.Next()||rows.Err()!=nil {t.Fatal("retained cardinality",rows.Err())};rows.Close();fixture.HeaderCheckColumns(t,descriptors[i],s.Columns,s.Types,false,true)
  }
  if e:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));e!=nil {t.Fatal(e)};checkOriginal();changeInputs();checkOriginal()
  // Fail after replaying the complete genuine catalog and a sentinel mutation:
  // neither staged replacement nor prefix mutation may become visible.
  before:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  bad:=dump+"\nINSERT INTO header_images VALUES(2,'\\x'::bytea,NULL,0);\nINSERT INTO header_images VALUES(3,NULL,NULL,1/0);\n"
  diagnostic:=fixture.ControlsReferenceDiagnostic("22012","division by zero")
  fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad)),&diagnostic)
  if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before {t.Fatal("failed restore replaced live header state")};checkOriginal()
 })
}
func TestReferencePageinspectHeaderControls(t *testing.T) {headerControls(t,false)}
func TestReferencePageinspectHeaderFeatureSmoke(t *testing.T) {headerControls(t,true)}

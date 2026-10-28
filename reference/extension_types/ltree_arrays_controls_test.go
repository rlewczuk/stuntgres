package reference_test

import (
 "bytes"
 "database/sql"
 "reflect"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func ltreeArraysProjection() (string,[]string,[]string,[]any) {
 columns,families,want:=make([]string,14),make([]string,14),make([]any,14)
 expressions:=make([]string,14)
 operand:=func(typ string) string {switch typ {case "ltree[]":return "paths";case "lquery[]":return "queries";case "ltree":return "target";case "lquery":return "q";default:return "x"}}
 for i,tc:=range ltreeArrayCases {
  columns[i]="v"+string(rune('a'+i));families[i]="BOOL";want[i]=true
  if i>=12 {families[i]="LTREE";want[i]=[]string{"Top","Top.Science"}[i-12]}
  expressions[i]=tc.expression(operand(tc.leftType),operand(tc.rightType),i%2==0)+" AS "+columns[i]
 }
 return strings.Join(expressions,","),columns,families,want
}
func ltreeArraysControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper();run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE EXTENSION ltree",0))
 run(fixture.Exec("CREATE TABLE la_control(id integer,paths ltree[],queries lquery[],target ltree,q lquery,x ltxtquery)",0))
 run(fixture.Exec("INSERT INTO la_control VALUES (1,'[0:2]={Top,Top.Science,Top.Science.Astro}','{Top.*}','Top.Science','Top.*','Top & Science'),(2,'{Other}','{Nope}','Top.Science','Top.*','Top & Science'),(3,NULL,NULL,NULL,NULL,NULL)",3))
 run(fixture.Exec("CREATE TABLE la_storage(id integer,paths ltree[],queries lquery[])",0))
 run(fixture.Exec(`INSERT INTO la_storage VALUES (1,'{a,NULL}','{a,NULL}'),(2,'{{a,b}}','{{a,b}}'),(3,'{}','{}'),(4,'[7:8]={"",a}','[0:1]={a,b}'),(5,NULL,NULL)`,5))
 projection,_,_,_:=ltreeArraysProjection()
 run(fixture.Exec("CREATE VIEW la_view AS SELECT "+projection+" FROM la_control WHERE paths @> target",0))
}
func ltreeArraysControlVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper();projection,columns,families,want:=ltreeArraysProjection()
 for _,query:=range []string{"SELECT "+projection+" FROM la_control WHERE id=1","SELECT "+strings.Join(columns,",")+" FROM la_view"} {
  step:=ltreeArraySelect(b,query,columns,families,[][]any{want});fixture.RunStep(t,p.Ctx,c,b.Recorder,step);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(step))
 }
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT id,paths::text AS paths,queries::text AS queries FROM la_storage ORDER BY id",[]string{"id","paths","queries"},[]string{"INT4","TEXT","TEXT"},[][]any{{int64(1),"{a,NULL}","{a,NULL}"},{int64(2),"{{a,b}}","{{a,b}}"},{int64(3),"{}","{}"},{int64(4),`[7:8]={"",a}`,"[0:1]={a,b}"},{int64(5),nil,nil}}))
}
func ltreeArraysConsume(t *testing.T,rows *sql.Rows,want []any) {
 t.Helper();defer rows.Close();if !rows.Next(){t.Fatal("retained row absent",rows.Err())}
 got,dest:=make([]any,len(want)),make([]any,len(want));for i:=range got {dest[i]=&got[i]};if err:=rows.Scan(dest...);err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(got,want){t.Fatalf("retained %#v want %#v",got,want)};if rows.Next() || rows.Err()!=nil{t.Fatal("retained trailing row",rows.Err())}
}
func TestReferenceLtreeArrayControls(t *testing.T) {
 t.Run("presentation",ltreeArraysPresentation)
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeArraysControlSetup(t,p,b,c);ltreeArraysControlVerify(t,p,b,c)
  if b.PG{return}
  projection,_,families,want:=ltreeArraysProjection()
  old,err:=b.DB.QueryContext(p.Ctx,"SELECT "+projection+" FROM la_control WHERE id=1");if err!=nil{t.Fatal(err)};defer old.Close()
  metadata:=ltreeArrayMetadata(t,b,old,families,nil);columns,err:=old.Columns();if err!=nil{t.Fatal(err)};detached:=append([]string(nil),columns...)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE la_notnull(paths ltree[] NOT NULL,queries lquery[] NOT NULL)",0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO la_notnull VALUES ('{a}','{a.*}')",1))
  type retainedMetadata struct {rows *sql.Rows;columns []*sql.ColumnType;families []string;nullable []bool;want []any;empty bool}
  var retained []retainedMetadata
  for _,tc:=range []struct{query string;families []string;nullable []bool;want []any}{
   {"SELECT paths,queries FROM la_control WHERE id=1",[]string{"_LTREE","_LQUERY"},[]bool{true,true},[]any{"[0:2]={Top,Top.Science,Top.Science.Astro}","{Top.*}"}},
   {"SELECT paths,queries FROM la_notnull",[]string{"_LTREE","_LQUERY"},[]bool{false,false},[]any{"{a}","{a.*}"}},
   {"SELECT "+strings.Join(columns,",")+" FROM la_view",families,[]bool{true,true,true,true,true,true,true,true,true,true,true,true,true,true},want},
   {"SELECT "+projection+" FROM la_control WHERE id=1",families,nil,want},
   {"SELECT NULL::ltree[] @> 'a'::ltree AS matched,NULL::ltree[] ?@> 'a'::ltree AS first FROM la_notnull",[]string{"BOOL","LTREE"},nil,[]any{nil,nil}},
  } {
   for _,empty:=range []bool{false,true} {
    query:=tc.query;if empty {if strings.Contains(query," WHERE "){query+=" AND false"}else{query+=" WHERE false"}}
    rows,err:=b.DB.QueryContext(p.Ctx,query);if err!=nil{t.Fatal(err)};defer rows.Close()
    descriptors:=ltreeArrayMetadata(t,b,rows,tc.families,tc.nullable)
    retained=append(retained,retainedMetadata{rows,descriptors,tc.families,tc.nullable,tc.want,empty})
   }
  }
  var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
  extension,table:=strings.Index(dump.String(),"CREATE EXTENSION"),strings.Index(dump.String(),"CREATE TABLE")
  if extension<0 || table<extension || strings.Contains(dump.String(),"CREATE FUNCTION") || strings.Contains(dump.String(),"CREATE OPERATOR") {t.Fatal("extension must own selected members and precede consumers")}
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
  if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
  ltreeArraysControlVerify(t,p,b,c);ltreeArraysConsume(t,old,want)
  if !reflect.DeepEqual(columns,detached) {t.Fatal("retained columns changed")}
  ltreeArrayColumnMetadata(t,b,metadata,families,nil)
  for _,saved:=range retained {
   ltreeArrayColumnMetadata(t,b,saved.columns,saved.families,saved.nullable)
   if saved.empty {
    if saved.rows.Next() || saved.rows.Err()!=nil {t.Fatal("retained empty result",saved.rows.Err())}
    if err:=saved.rows.Close();err!=nil{t.Fatal(err)}
   } else {ltreeArraysConsume(t,saved.rows,saved.want)}
   ltreeArrayColumnMetadata(t,b,saved.columns,saved.families,saved.nullable)
  }
  target:=fixture.NewFixture(t,false,true)
  fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("CREATE TABLE la_sentinel(v integer)",0))
  if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
  destination:=fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
  ltreeArraysControlVerify(t,target,destination,target.Memory)
  for _,suffix:=range []string{"CREATE TABLE la_broken(v boolean); INSERT INTO la_broken VALUES ('{a,NULL}'::ltree[] @> 'a'::ltree);","CREATE TABLE la_broken(v ltree[]); INSERT INTO la_broken VALUES ('{a..b}');"} {
   bad:=dump.String()+"\n"+suffix
   err:=stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad))
   if strings.Contains(suffix,"boolean") {want:=fixture.ControlsReferenceDiagnostic("22004","array must not contain nulls");fixture.AssertDiagnostic(t,err,&want)} else {want:=fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3");want.Position=ltreeSQLPosition(bad,"'{a..b}'");fixture.AssertDiagnostic(t,err,&want)}
   ltreeArraysControlVerify(t,target,destination,target.Memory)
  }
  var again bytes.Buffer;if err:=stuntgres.Dump(target.Ctx,target.Native,&again);err!=nil{t.Fatal(err)};if !bytes.Equal(again.Bytes(),dump.Bytes()){t.Fatal("restore round trip changed dump")}
 })
}
func TestReferenceLtreeArrayReplay(t *testing.T) {
 p:=fixture.NewScenario(t)
 native:=fixture.Backend{DB:p.Memory,Recorder:p.MemoryRecord}
 ltreeArraysControlSetup(t,p,native,p.Memory)
 var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
 ltreeArraysControlVerify(t,p,native,p.Memory)
 if p.Postgres!=nil {
  if _,err:=p.Postgres.ExecContext(p.Ctx,dump.String());err!=nil{t.Fatal("actual emitted SQL",err)}
  pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord};ltreeArraysControlVerify(t,p,pg,p.Postgres)
 }
}
